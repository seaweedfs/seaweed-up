package manager

import (
	"bytes"
	"encoding/json"
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
	// healthURL is the post-upgrade HTTP probe; empty falls back to a
	// `systemctl is-active` gate (components with no HTTP listener, e.g. workers).
	healthURL string
	// healthCodes is the case-glob of accepted HTTP statuses; "" means "2??".
	healthCodes string
	describe    string
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
	// extras returns component-specific config files (e.g. s3.json) to upload.
	// Called fresh per attempt since each extra holds a single-use Reader. May be nil.
	extras func() ([]extraConfigFile, error)
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

	targets := buildUpgradeTargets(specification, scheme)

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

	// Resolve the default admin endpoint workers fall back to when they carry
	// no explicit admin of their own (same precedence as DeployCluster). Only
	// needed when the cluster actually has workers.
	var workerAdmins []string
	if len(specification.WorkerServers) > 0 {
		resolved, err := resolveWorkerDefaultAdmins(specification)
		if err != nil {
			return err
		}
		workerAdmins = resolved
	}

	info(fmt.Sprintf("Starting rolling upgrade to version %q (previous=%q)", targetVersion, previousVersion))

	// rollbackHost reinstalls the given previous version on t, if any.
	rollbackHost := func(t upgradeTarget, prev string, cause error) error {
		if !opts.RollbackOnFailure || prev == "" {
			return cause
		}
		info(fmt.Sprintf("Rolling back %s to version %q", t.describe, prev))
		m.Version = prev
		// Re-resolve against prev (never "dev") to clear m.devAsset, else the
		// rollback would reinstall the dev build via install.sh's dev path.
		if err := m.resolveDevAsset(prev); err != nil {
			return fmt.Errorf("%s failed (%v); resolving rollback target %q: %w", t.describe, cause, prev, err)
		}
		if rbErr := m.upgradeOneHost(specification, masters, workerAdmins, t); rbErr != nil {
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
		if err := m.upgradeOneHost(specification, masters, workerAdmins, t); err != nil {
			return rollbackHost(t, hostPrev, fmt.Errorf("upgrade %s: %w", t.describe, err))
		}

		sshAddr := net.JoinHostPort(t.ip, strconv.Itoa(t.portSsh))
		var healthErr error
		if t.healthURL != "" {
			healthErr = m.waitForHealthyViaSSH(sshAddr, t.healthURL, t.healthCodes, opts.HealthTimeout, opts.HealthInterval, opts.InsecureSkipTLSVerify)
		} else {
			// No HTTP listener (workers): fall back to a systemd liveness gate.
			unit := fmt.Sprintf("seaweed_%s%d.service", t.component, t.index)
			healthErr = m.waitForServiceActiveViaSSH(sshAddr, unit, opts.HealthTimeout, opts.HealthInterval)
		}
		if healthErr != nil {
			info(fmt.Sprintf("Health check failed for %s: %v", t.describe, healthErr))
			return rollbackHost(t, hostPrev, fmt.Errorf("health check failed for %s: %w", t.describe, healthErr))
		}

		info(fmt.Sprintf("%s upgraded and healthy", t.describe))
	}

	m.Version = targetVersion
	info(fmt.Sprintf("Rolling upgrade to %q complete", targetVersion))
	return nil
}

// buildUpgradeTargets lists the instances a rolling upgrade touches, in
// dependency order: volume -> filer -> master (masters next-to-last for quorum)
// -> s3 -> admin -> worker (workers last; they connect to the admin).
//
// healthURL is the node's advertised Ip:port (not loopback) because the probe
// runs on the node over SSH and weed binds to the advertised -ip. Liveness
// codes differ per component: admin root 307-redirects (2xx|3xx), s3's API 403s
// so we probe /status (2xx), and workers have no HTTP port (empty URL -> the
// caller's systemctl is-active gate).
func buildUpgradeTargets(specification *spec.Specification, scheme string) []upgradeTarget {
	var targets []upgradeTarget
	for i, v := range specification.VolumeServers {
		hostPort := net.JoinHostPort(v.Ip, strconv.Itoa(v.Port))
		targets = append(targets, upgradeTarget{
			component: "volume", index: i, ip: v.Ip, portSsh: v.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/status", scheme, hostPort),
			describe:  fmt.Sprintf("volume%d %s", i, hostPort),
		})
	}
	for i, f := range specification.FilerServers {
		hostPort := net.JoinHostPort(f.Ip, strconv.Itoa(f.Port))
		targets = append(targets, upgradeTarget{
			component: "filer", index: i, ip: f.Ip, portSsh: f.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/", scheme, hostPort),
			describe:  fmt.Sprintf("filer%d %s", i, hostPort),
		})
	}
	for i, ms := range specification.MasterServers {
		hostPort := net.JoinHostPort(ms.Ip, strconv.Itoa(ms.Port))
		targets = append(targets, upgradeTarget{
			component: "master", index: i, ip: ms.Ip, portSsh: ms.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/cluster/status", scheme, hostPort),
			describe:  fmt.Sprintf("master%d %s", i, hostPort),
		})
	}
	for i, s3 := range specification.S3Servers {
		hostPort := net.JoinHostPort(s3.Ip, strconv.Itoa(s3.Port))
		targets = append(targets, upgradeTarget{
			component: "s3", index: i, ip: s3.Ip, portSsh: s3.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/status", scheme, hostPort),
			describe:  fmt.Sprintf("s3%d %s", i, hostPort),
		})
	}
	for i, a := range specification.AdminServers {
		hostPort := net.JoinHostPort(a.Ip, strconv.Itoa(a.Port))
		targets = append(targets, upgradeTarget{
			component: "admin", index: i, ip: a.Ip, portSsh: a.PortSsh,
			healthURL: fmt.Sprintf("%s://%s/", scheme, hostPort), healthCodes: "2??|3??",
			describe: fmt.Sprintf("admin%d %s", i, hostPort),
		})
	}
	for i, w := range specification.WorkerServers {
		hostPort := net.JoinHostPort(w.Ip, strconv.Itoa(w.PortSsh))
		targets = append(targets, upgradeTarget{
			component: "worker", index: i, ip: w.Ip, portSsh: w.PortSsh,
			describe: fmt.Sprintf("worker%d %s", i, hostPort),
		})
	}
	return targets
}

// upgradeOneHost stops, reinstalls (at m.Version), and starts a single host instance.
// workerAdmins is the resolved default admin endpoint list used when a worker
// spec carries no explicit admin (mirrors DeployCluster); it is unused for
// non-worker components.
func (m *Manager) upgradeOneHost(specification *spec.Specification, masters, workerAdmins []string, t upgradeTarget) error {
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
	case "s3":
		s3 := specification.S3Servers[t.index]
		// The s3 gateway's options reference an s3.json (IAM creds) by its
		// on-host path; re-render both so a credential change in the spec is
		// picked up by the upgrade. The path mirrors DeployS3Server.
		s3ConfigPath := ""
		if len(s3.S3Config) > 0 {
			s3ConfigPath = fmt.Sprintf("%s/s3%d.d/s3.json", m.confDir, t.index)
		}
		hooks = componentHooks{
			serviceName: "s3",
			sshAddr:     net.JoinHostPort(s3.Ip, strconv.Itoa(s3.PortSsh)),
			stop:        func() error { return m.StopS3Server(s3, t.index) },
			writeConfig: func(buf *bytes.Buffer) { s3.WriteToBuffer(buf, s3ConfigPath) },
			extras: func() ([]extraConfigFile, error) {
				if len(s3.S3Config) == 0 {
					return nil, nil
				}
				b, err := json.MarshalIndent(s3.S3Config, "", "  ")
				if err != nil {
					return nil, fmt.Errorf("marshal s3.json: %w", err)
				}
				// s3.json holds IAM credentials; restrict to owner-only.
				return []extraConfigFile{{Name: "s3.json", Content: bytes.NewBuffer(b), Mode: "0600"}}, nil
			},
		}
	case "admin":
		a := specification.AdminServers[t.index]
		hooks = componentHooks{
			serviceName: "admin",
			sshAddr:     net.JoinHostPort(a.Ip, strconv.Itoa(a.PortSsh)),
			stop:        func() error { return m.StopAdminServer(a, t.index) },
			writeConfig: func(buf *bytes.Buffer) { a.WriteToBuffer(masters, buf) },
		}
	case "worker":
		w := specification.WorkerServers[t.index]
		hooks = componentHooks{
			serviceName: "worker",
			sshAddr:     net.JoinHostPort(w.Ip, strconv.Itoa(w.PortSsh)),
			stop:        func() error { return m.StopWorkerServer(w, t.index) },
			writeConfig: func(buf *bytes.Buffer) { w.WriteToBuffer(workerAdmins, buf) },
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
	deploy := func() error {
		return operator.ExecuteRemote(hooks.sshAddr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
			var buf bytes.Buffer
			hooks.writeConfig(&buf)
			var extras []extraConfigFile
			if hooks.extras != nil {
				e, err := hooks.extras()
				if err != nil {
					return err
				}
				extras = e
			}
			if hooks.prepareRemote != nil {
				if err := hooks.prepareRemote(op); err != nil {
					return err
				}
			}
			if err := m.deployComponentInstance(op, hooks.serviceName, componentInstance, &buf, extras...); err != nil {
				return err
			}
			return m.sudo(op, fmt.Sprintf("systemctl restart seaweed_%s.service", componentInstance))
		})
	}
	// Retry on transient SSH/SCP errors (a bastion can drop a multiplexed
	// channel mid-transfer). Every step is idempotent and ExecuteRemote
	// re-dials, so a fresh attempt is safe.
	const attempts = 3
	var lastErr error
	for i := 0; i < attempts; i++ {
		if lastErr = deploy(); lastErr == nil {
			return nil
		}
		if i < attempts-1 {
			info(fmt.Sprintf("deploy %s attempt %d/%d failed: %v (retrying)", t.describe, i+1, attempts, lastErr))
		}
	}
	return lastErr
}

// waitForHealthyViaSSH curls probeURL from the node itself (over SSH, so it
// tunnels through the bastion to the node's private address) until it returns a
// status matching codes ("2??", "2??|3??", …) or the timeout elapses. The poll
// loop runs in a single SSH session. insecureSkipTLSVerify adds curl -k.
// TODO: use cluster CA once tls bootstrap PR lands.
func (m *Manager) waitForHealthyViaSSH(sshAddr, probeURL, codes string, timeout, interval time.Duration, insecureSkipTLSVerify bool) error {
	if probeURL == "" {
		return nil
	}
	if codes == "" {
		codes = "2??"
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
	// curl errors (e.g. connection-refused while starting) collapse to "000".
	script := `end=$(( $(date +%s) + __SECS__ ))
last=000
while [ "$(date +%s)" -lt "$end" ]; do
  code=$(curl -sS __K__-o /dev/null -m 5 -w '%{http_code}' __URL__ 2>/dev/null || echo 000)
  case "$code" in __CODES__) echo HEALTHY; exit 0;; esac
  last=$code
  sleep __IV__
done
echo "UNHEALTHY last=$last"
exit 1`
	script = strings.ReplaceAll(script, "__SECS__", strconv.Itoa(secs))
	script = strings.ReplaceAll(script, "__IV__", strconv.Itoa(iv))
	script = strings.ReplaceAll(script, "__K__", kFlag)
	script = strings.ReplaceAll(script, "__CODES__", codes)
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

// waitForServiceActiveViaSSH polls `systemctl is-active <unit>` until "active"
// or timeout. It's the gate for components with no HTTP listener (workers);
// a crash-looping unit never settles on active.
func (m *Manager) waitForServiceActiveViaSSH(sshAddr, unit string, timeout, interval time.Duration) error {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	iv := int(interval.Seconds())
	if iv < 1 {
		iv = 1
	}
	script := `end=$(( $(date +%s) + __SECS__ ))
last=unknown
while [ "$(date +%s)" -lt "$end" ]; do
  st=$(systemctl is-active __UNIT__ 2>/dev/null || true)
  if [ "$st" = active ]; then echo ACTIVE; exit 0; fi
  last=$st
  sleep __IV__
done
echo "INACTIVE last=$last"
exit 1`
	script = strings.ReplaceAll(script, "__SECS__", strconv.Itoa(secs))
	script = strings.ReplaceAll(script, "__IV__", strconv.Itoa(iv))
	script = strings.ReplaceAll(script, "__UNIT__", shellSingleQuote(unit))

	var out []byte
	err := operator.ExecuteRemote(sshAddr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		b, e := op.Output(script)
		out = b
		return e
	})
	if err != nil {
		return fmt.Errorf("service %s on %s not active: %w (%s)", unit, sshAddr, err, strings.TrimSpace(string(out)))
	}
	return nil
}
