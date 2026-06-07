package manager

import (
	"bytes"
	"fmt"
	"net"
	"strconv"
	"strings"
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
	// prepareRemote runs component-specific host prep inside the SSH session
	// (e.g. creating volume -dir paths) before deployComponentInstance runs.
	// May be nil.
	prepareRemote func(op operator.CommandOperator) error
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

	// When upgrading to the rolling "dev" tag, resolve it to the newest
	// concrete dated build now (keyed on targetVersion, not m.Version which
	// still holds the probed current version for rollback). The resolved
	// asset + its build id drive the download and content-based skip check.
	if err := m.resolveDevAsset(targetVersion); err != nil {
		return fmt.Errorf("resolve dev build: %w", err)
	}

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

	// healthURL targets the node's own configured address (Ip:port). The
	// probe runs on the host itself over SSH (see waitForHealthyViaSSH), so
	// it tunnels through the bastion like every other operation instead of
	// needing direct HTTP reachability to the node's (often private) address
	// from the control machine. We use the configured Ip rather than
	// localhost because weed binds its HTTP listener to the advertised -ip
	// (e.g. 10.0.0.1), so loopback is refused on hosts that don't bind 0.0.0.0.
	//
	// Volume servers first.
	for i, v := range specification.VolumeServers {
		hostPort := net.JoinHostPort(v.Ip, strconv.Itoa(v.Port))
		targets = append(targets, upgradeTarget{
			component: "volume",
			index:     i,
			ip:        v.Ip,
			portSsh:   v.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/status", scheme, hostPort),
			describe:  fmt.Sprintf("volume%d %s", i, hostPort),
		})
	}
	// Filer servers next.
	for i, f := range specification.FilerServers {
		hostPort := net.JoinHostPort(f.Ip, strconv.Itoa(f.Port))
		targets = append(targets, upgradeTarget{
			component: "filer",
			index:     i,
			ip:        f.Ip,
			portSsh:   f.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/", scheme, hostPort),
			describe:  fmt.Sprintf("filer%d %s", i, hostPort),
		})
	}
	// Masters last so quorum isn't disturbed early.
	for i, ms := range specification.MasterServers {
		hostPort := net.JoinHostPort(ms.Ip, strconv.Itoa(ms.Port))
		targets = append(targets, upgradeTarget{
			component: "master",
			index:     i,
			ip:        ms.Ip,
			portSsh:   ms.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/cluster/status", scheme, hostPort),
			describe:  fmt.Sprintf("master%d %s", i, hostPort),
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
		masters = append(masters, net.JoinHostPort(masterSpec.Ip, strconv.Itoa(masterSpec.Port)))
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

		sshAddr := net.JoinHostPort(t.ip, strconv.Itoa(t.portSsh))
		if err := m.waitForHealthyViaSSH(sshAddr, t.healthURL, opts.HealthTimeout, opts.HealthInterval, opts.InsecureSkipTLSVerify); err != nil {
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
			serviceName:   "volume",
			sshAddr:       net.JoinHostPort(vs.Ip, strconv.Itoa(vs.PortSsh)),
			stop:          func() error { return m.StopVolumeServer(vs, t.index) },
			writeConfig:   func(buf *bytes.Buffer) { vs.WriteToBuffer(masters, buf) },
			prepareRemote: func(op operator.CommandOperator) error { return m.ensureVolumeFolders(op, vs) },
		}
	case "filer":
		fs := specification.FilerServers[t.index]
		hooks = componentHooks{
			serviceName: "filer",
			sshAddr:     net.JoinHostPort(fs.Ip, strconv.Itoa(fs.PortSsh)),
			stop:        func() error { return m.StopFilerServer(fs, t.index) },
			writeConfig: func(buf *bytes.Buffer) { fs.WriteToBuffer(masters, buf) },
		}
	case "master":
		ms := specification.MasterServers[t.index]
		hooks = componentHooks{
			serviceName: "master",
			sshAddr:     net.JoinHostPort(ms.Ip, strconv.Itoa(ms.PortSsh)),
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
		if hooks.prepareRemote != nil {
			if err := hooks.prepareRemote(op); err != nil {
				return err
			}
		}
		if err := m.deployComponentInstance(op, hooks.serviceName, componentInstance, &buf); err != nil {
			return err
		}
		return m.sudo(op, fmt.Sprintf("systemctl restart seaweed_%s.service", componentInstance))
	})
}

// waitForHealthyViaSSH polls probeURL from the node itself (over SSH) until it
// returns 2xx or the timeout elapses. Running the probe on the node — rather
// than issuing an HTTP request from the control machine — means it tunnels
// through the bastion exactly like every other operation, so it works even
// when the node's (often private) service address is unreachable from the
// laptop running the upgrade.
//
// The poll loop runs inside a single SSH session (one shell loop) rather than
// one SSH round-trip per attempt, so a slow-starting service doesn't cost a
// handshake per second.
//
// Pass insecureSkipTLSVerify=true to add curl's -k for self-signed dev
// clusters; callers must opt in explicitly via --insecure-skip-tls-verify.
//
// TODO: use cluster CA once tls bootstrap PR lands.
func (m *Manager) waitForHealthyViaSSH(sshAddr, probeURL string, timeout, interval time.Duration, insecureSkipTLSVerify bool) error {
	if probeURL == "" {
		return nil
	}
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	iv := int(interval.Seconds())
	if iv < 1 {
		iv = 1
	}
	kFlag := ""
	if insecureSkipTLSVerify {
		kFlag = "-k "
	}
	// POSIX-sh poll loop: curl the endpoint until it answers 2xx or the
	// deadline passes. `2??` matches any 2xx status; curl errors (e.g.
	// connection refused while the unit is still starting) are swallowed to
	// "000" so the loop keeps trying.
	script := `end=$(( $(date +%s) + __SECS__ ))
last=000
while [ "$(date +%s)" -lt "$end" ]; do
  code=$(curl -sS __K__-o /dev/null -m 5 -w '%{http_code}' __URL__ 2>/dev/null || echo 000)
  case "$code" in 2??) echo HEALTHY; exit 0;; esac
  last=$code
  sleep __IV__
done
echo "UNHEALTHY last=$last"
exit 1`
	script = strings.ReplaceAll(script, "__SECS__", strconv.Itoa(secs))
	script = strings.ReplaceAll(script, "__IV__", strconv.Itoa(iv))
	script = strings.ReplaceAll(script, "__K__", kFlag)
	script = strings.ReplaceAll(script, "__URL__", shellSingleQuote(probeURL))

	var out []byte
	err := operator.ExecuteRemote(sshAddr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		b, e := op.Output(script)
		out = b
		return e
	})
	if err != nil {
		return fmt.Errorf("probe %s on %s: %w (%s)", probeURL, sshAddr, err, strings.TrimSpace(string(out)))
	}
	return nil
}
