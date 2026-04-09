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
	// InsecureSkipTLSVerify disables TLS certificate verification for health
	// probes. Defaults to false; only set when the caller explicitly opts in.
	InsecureSkipTLSVerify bool
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

// componentHooks bundles the per-component knobs used by upgradeOneHost so
// the stop/redeploy/restart logic can live in a single helper instead of
// being copy-pasted per component.
type componentHooks struct {
	// serviceName is the systemd unit base ("volume", "filer", "master").
	serviceName string
	// sshAddr is host:port for the SSH target of this instance.
	sshAddr string
	// stop stops the running systemd unit best-effort.
	stop func() error
	// writeConfig writes the component-specific CLI options to buf.
	writeConfig func(buf *bytes.Buffer)
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

	// rollbackHost reinstalls the given previous version on t, if any.
	rollbackHost := func(t upgradeTarget, prev string, cause error) error {
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

		// Capture the version running on this host right before we touch it.
		// Today this is homogeneous across the cluster (sourced from
		// previousVersion) but the capture happens here so the timing matches
		// the semantics: the rollback target is whatever was running on this
		// specific host immediately before its stop/install step.
		hostPrev := previousVersion

		m.Version = targetVersion
		if err := m.upgradeOneHost(specification, masters, t); err != nil {
			return rollbackHost(t, hostPrev, fmt.Errorf("upgrade %s: %w", t.describe, err))
		}

		if err := waitForHealthy(t.healthURL, opts.HealthTimeout, opts.HealthInterval, opts.InsecureSkipTLSVerify); err != nil {
			info(fmt.Sprintf("Health check failed for %s: %v", t.describe, err))
			return rollbackHost(t, hostPrev, fmt.Errorf("health check failed for %s: %w", t.describe, err))
		}

		info(fmt.Sprintf("%s upgraded and healthy", t.describe))
	}

	m.Version = targetVersion
	info(fmt.Sprintf("Rolling upgrade to %q complete", targetVersion))
	return nil
}

// upgradeOneHost stops, reinstalls (at m.Version), and starts a single host instance.
func (m *Manager) upgradeOneHost(specification *spec.Specification, masters []string, t upgradeTarget) error {
	var hooks componentHooks
	switch t.component {
	case "volume":
		vs := specification.VolumeServers[t.index]
		hooks = componentHooks{
			serviceName: "volume",
			sshAddr:     fmt.Sprintf("%s:%d", vs.Ip, vs.PortSsh),
			stop:        func() error { return m.StopVolumeServer(vs, t.index) },
			writeConfig: func(buf *bytes.Buffer) { vs.WriteToBuffer(masters, buf) },
		}
	case "filer":
		fs := specification.FilerServers[t.index]
		hooks = componentHooks{
			serviceName: "filer",
			sshAddr:     fmt.Sprintf("%s:%d", fs.Ip, fs.PortSsh),
			stop:        func() error { return m.StopFilerServer(fs, t.index) },
			writeConfig: func(buf *bytes.Buffer) { fs.WriteToBuffer(masters, buf) },
		}
	case "master":
		ms := specification.MasterServers[t.index]
		hooks = componentHooks{
			serviceName: "master",
			sshAddr:     fmt.Sprintf("%s:%d", ms.Ip, ms.PortSsh),
			stop:        func() error { return m.StopMasterServer(ms, t.index) },
			writeConfig: func(buf *bytes.Buffer) { ms.WriteToBuffer(masters, buf) },
		}
	default:
		return fmt.Errorf("unknown component: %s", t.component)
	}
	return m.runUpgradeHost(t, hooks)
}

// runUpgradeHost applies the stop/redeploy/restart sequence to a single host
// using the component-specific hooks. This holds the logic that was previously
// duplicated across the volume/filer/master switch cases.
func (m *Manager) runUpgradeHost(t upgradeTarget, hooks componentHooks) error {
	if err := hooks.stop(); err != nil {
		// Best-effort stop: log and continue to reinstall which will restart the unit.
		info(fmt.Sprintf("stop %s returned: %v (continuing)", t.describe, err))
	}
	componentInstance := fmt.Sprintf("%s%d", hooks.serviceName, t.index)
	return operator.ExecuteRemote(hooks.sshAddr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		var buf bytes.Buffer
		hooks.writeConfig(&buf)
		if err := m.deployComponentInstance(op, hooks.serviceName, componentInstance, &buf); err != nil {
			return err
		}
		return m.sudo(op, fmt.Sprintf("systemctl restart seaweed_%s.service", componentInstance))
	})
}

// waitForHealthy polls an HTTP(S) URL until it returns 2xx or the timeout
// elapses. This is intentionally minimal — a more thorough probe would parse
// the body.
//
// By default TLS connections verify against the system cert pool. Pass
// insecureSkipTLSVerify=true to disable verification (for self-signed dev
// clusters); callers must opt in explicitly via --insecure-skip-tls-verify.
//
// TODO: use cluster CA once tls bootstrap PR lands.
func waitForHealthy(url string, timeout, interval time.Duration, insecureSkipTLSVerify bool) error {
	if url == "" {
		return nil
	}
	tlsConfig := &tls.Config{}
	if insecureSkipTLSVerify {
		tlsConfig.InsecureSkipVerify = true //nolint:gosec // explicitly requested via --insecure-skip-tls-verify
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
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
