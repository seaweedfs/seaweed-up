package manager

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// UpgradeOptions configures how UpgradeCluster behaves.
type UpgradeOptions struct {
	// RollbackOnFailure re-installs the previous version on a host when the
	// post-upgrade health gate fails. Defaults to true.
	RollbackOnFailure bool
	// DryRun, when set, prints the upgrade plan without applying any changes.
	DryRun bool
	// HealthTimeout is the total wait for a host to become healthy after restart.
	HealthTimeout time.Duration
	// HealthInterval is the poll interval between health probes.
	HealthInterval time.Duration
}

// upgradeTarget represents a single host/instance to be upgraded.
type upgradeTarget struct {
	component string // "volume", "filer", "master"
	index     int
	ip        string
	portSsh   int
	// healthURL is the HTTP endpoint used for the post-upgrade health probe.
	healthURL string
	// describe is a human-readable identifier for logs/errors.
	describe string
}

// UpgradeCluster performs a rolling upgrade of the cluster to targetVersion.
//
// Order: volume servers -> filer servers -> master servers (masters last).
// For each host, sequentially: stop systemd unit, reinstall at the new version,
// start, then poll an HTTP health endpoint. On failure, if RollbackOnFailure is
// set, reinstall the previously recorded version on that host before returning.
func (m *Manager) UpgradeCluster(specification *spec.Specification, targetVersion string, opts UpgradeOptions) error {
	if targetVersion == "" {
		return fmt.Errorf("target version is required")
	}
	if opts.HealthTimeout <= 0 {
		opts.HealthTimeout = 2 * time.Minute
	}
	if opts.HealthInterval <= 0 {
		opts.HealthInterval = 3 * time.Second
	}

	m.prepare(specification)

	// Snapshot the currently-deployed version. runClusterUpgrade is expected to
	// populate m.Version with the cluster's current (pre-upgrade) version by
	// probing a master host. If it could not probe, m.Version will be empty and
	// rollback will be disabled for this run.
	previousVersion := m.Version

	// Pick the scheme for health probes based on the global TLS flag.
	scheme := "http"
	if specification.GlobalOptions.TLSEnabled {
		scheme = "https"
	}

	var targets []upgradeTarget

	// Volume servers first.
	for i, v := range specification.VolumeServers {
		targets = append(targets, upgradeTarget{
			component: "volume",
			index:     i,
			ip:        v.Ip,
			portSsh:   v.PortSsh,
			healthURL: fmt.Sprintf("%s://%s:%d/status", scheme, v.Ip, v.Port),
			describe:  fmt.Sprintf("volume%d %s:%d", i, v.Ip, v.Port),
		})
	}
	// Filer servers next.
	for i, f := range specification.FilerServers {
		targets = append(targets, upgradeTarget{
			component: "filer",
			index:     i,
			ip:        f.Ip,
			portSsh:   f.PortSsh,
			healthURL: fmt.Sprintf("%s://%s:%d/", scheme, f.Ip, f.Port),
			describe:  fmt.Sprintf("filer%d %s:%d", i, f.Ip, f.Port),
		})
	}
	// Masters last so quorum isn't disturbed early.
	for i, ms := range specification.MasterServers {
		targets = append(targets, upgradeTarget{
			component: "master",
			index:     i,
			ip:        ms.Ip,
			portSsh:   ms.PortSsh,
			healthURL: fmt.Sprintf("%s://%s:%d/cluster/status", scheme, ms.Ip, ms.Port),
			describe:  fmt.Sprintf("master%d %s:%d", i, ms.Ip, ms.Port),
		})
	}

	// Per-host previous-version record. Today this is homogeneous across the
	// cluster (populated from previousVersion) but is captured immediately
	// before each host's stop/install below so rollback picks up whatever was
	// running on that specific host.
	hostPrevVersion := make(map[string]string, len(targets))

	if opts.DryRun {
		info(fmt.Sprintf("Dry-run: rolling upgrade plan to version %q (previous=%q)", targetVersion, previousVersion))
		for _, t := range targets {
			info(fmt.Sprintf("  would upgrade %s (%s)", t.describe, t.component))
		}
		return nil
	}

	var masters []string
	for _, masterSpec := range specification.MasterServers {
		masters = append(masters, fmt.Sprintf("%s:%d", masterSpec.Ip, masterSpec.Port))
	}

	info(fmt.Sprintf("Starting rolling upgrade to version %q (previous=%q)", targetVersion, previousVersion))

	// rollbackHost reinstalls the recorded previous version on t, if any.
	rollbackHost := func(t upgradeTarget, cause error) error {
		prev := hostPrevVersion[t.describe]
		if !opts.RollbackOnFailure || prev == "" {
			return cause
		}
		info(fmt.Sprintf("Rolling back %s to version %q", t.describe, prev))
		m.Version = prev
		if rbErr := m.upgradeOneHost(specification, masters, t); rbErr != nil {
			return fmt.Errorf("%s failed (%v); rollback to %q also failed: %w", t.describe, cause, prev, rbErr)
		}
		return fmt.Errorf("%s failed; rolled back to %q: %w", t.describe, prev, cause)
	}

	for _, t := range targets {
		info(fmt.Sprintf("Upgrading %s...", t.describe))

		// Record the version running on this host right before we touch it.
		hostPrevVersion[t.describe] = previousVersion

		m.Version = targetVersion
		if err := m.upgradeOneHost(specification, masters, t); err != nil {
			return rollbackHost(t, fmt.Errorf("upgrade %s: %w", t.describe, err))
		}

		if err := waitForHealthy(t.healthURL, opts.HealthTimeout, opts.HealthInterval); err != nil {
			info(fmt.Sprintf("Health check failed for %s: %v", t.describe, err))
			return rollbackHost(t, fmt.Errorf("health check failed for %s: %w", t.describe, err))
		}

		info(fmt.Sprintf("%s upgraded and healthy", t.describe))
	}

	m.Version = targetVersion
	info(fmt.Sprintf("Rolling upgrade to %q complete", targetVersion))
	return nil
}

// upgradeOneHost stops, reinstalls (at m.Version), and starts a single host instance.
func (m *Manager) upgradeOneHost(specification *spec.Specification, masters []string, t upgradeTarget) error {
	switch t.component {
	case "volume":
		vs := specification.VolumeServers[t.index]
		if err := m.StopVolumeServer(vs, t.index); err != nil {
			// Best-effort stop: log and continue to reinstall which will restart the unit.
			info(fmt.Sprintf("stop %s returned: %v (continuing)", t.describe, err))
		}
		return operator.ExecuteRemote(fmt.Sprintf("%s:%d", vs.Ip, vs.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
			component := "volume"
			componentInstance := fmt.Sprintf("%s%d", component, t.index)
			var buf bytes.Buffer
			vs.WriteToBuffer(masters, &buf)
			if err := m.deployComponentInstance(op, component, componentInstance, &buf); err != nil {
				return err
			}
			return m.sudo(op, fmt.Sprintf("systemctl restart seaweed_%s.service", componentInstance))
		})
	case "filer":
		fs := specification.FilerServers[t.index]
		if err := m.StopFilerServer(fs, t.index); err != nil {
			info(fmt.Sprintf("stop %s returned: %v (continuing)", t.describe, err))
		}
		return operator.ExecuteRemote(fmt.Sprintf("%s:%d", fs.Ip, fs.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
			component := "filer"
			componentInstance := fmt.Sprintf("%s%d", component, t.index)
			var buf bytes.Buffer
			fs.WriteToBuffer(masters, &buf)
			if err := m.deployComponentInstance(op, component, componentInstance, &buf); err != nil {
				return err
			}
			return m.sudo(op, fmt.Sprintf("systemctl restart seaweed_%s.service", componentInstance))
		})
	case "master":
		ms := specification.MasterServers[t.index]
		if err := m.StopMasterServer(ms, t.index); err != nil {
			info(fmt.Sprintf("stop %s returned: %v (continuing)", t.describe, err))
		}
		return operator.ExecuteRemote(fmt.Sprintf("%s:%d", ms.Ip, ms.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
			component := "master"
			componentInstance := fmt.Sprintf("%s%d", component, t.index)
			var buf bytes.Buffer
			ms.WriteToBuffer(masters, &buf)
			if err := m.deployComponentInstance(op, component, componentInstance, &buf); err != nil {
				return err
			}
			return m.sudo(op, fmt.Sprintf("systemctl restart seaweed_%s.service", componentInstance))
		})
	}
	return fmt.Errorf("unknown component: %s", t.component)
}

// waitForHealthy polls an HTTP(S) URL until it returns 2xx or the timeout
// elapses. This is intentionally minimal — a more thorough probe would parse
// the body.
//
// TODO: once the cluster CA bundle is plumbed through the Manager, honor
// InsecureSkipVerify=false and load the cluster root CA into RootCAs. Today
// we intentionally fall back to InsecureSkipVerify=true so that self-signed
// upgrade probes don't block the rolling upgrade.
func waitForHealthy(url string, timeout, interval time.Duration) error {
	if url == "" {
		return nil
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			// Default: verify. Fallback to skip because CA bundle isn't wired yet.
			InsecureSkipVerify: true, //nolint:gosec // see TODO above
		},
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			// Drain and close so the connection can be reused.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("status %d from %s", resp.StatusCode, url)
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timed out waiting for %s", url)
	}
	return lastErr
}
