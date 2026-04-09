package manager

import (
	"fmt"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
	"github.com/thanhpk/randstr"
)

// PrepareHostsForSpec normalizes the specification (applying SSH port defaults)
// and then runs host preparation on every host. This is intended to be called
// by `cluster prepare` when no deploy step follows.
func PrepareHostsForSpec(m *Manager, specification *spec.Specification) error {
	m.prepare(specification)
	return m.PrepareAllHosts(specification)
}

// PrepareHost uploads and runs the host preparation script on the target host
// reached through the given CommandOperator. The script configures ulimits,
// sysctls, firewall rules, and time sync. It is idempotent and safe to run
// multiple times.
func (m *Manager) PrepareHost(op operator.CommandOperator) error {
	info("Running host preparation (ulimits, sysctls, firewall, time sync)...")

	dir := "/tmp/seaweed-up." + randstr.String(6)
	defer func() { _ = op.Execute("rm -rf " + dir) }()

	if err := op.Execute("mkdir -p " + dir); err != nil {
		return fmt.Errorf("create tmp dir: %w", err)
	}

	remotePath := fmt.Sprintf("%s/host_prep.sh", dir)
	if err := op.Upload(strings.NewReader(scripts.HostPrepScript), remotePath, "0755"); err != nil {
		return fmt.Errorf("upload host_prep.sh: %w", err)
	}

	cmd := fmt.Sprintf("cat %s | SUDO_PASS=\"%s\" sh -", remotePath, m.sudoPass)
	if err := op.Execute(cmd); err != nil {
		return fmt.Errorf("run host_prep.sh: %w", err)
	}

	info("Host preparation complete.")
	return nil
}

// PrepareAllHosts runs PrepareHost on every unique host in the specification.
// Hosts are deduplicated by "ip:sshPort" so that colocated components are only
// prepared once. It returns the first error encountered, if any.
func (m *Manager) PrepareAllHosts(specification *spec.Specification) error {
	type hostKey struct {
		ip      string
		sshPort int
	}
	seen := map[hostKey]bool{}
	order := []hostKey{}

	add := func(ip string, port int) {
		if port == 0 {
			port = 22
		}
		k := hostKey{ip: ip, sshPort: port}
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
	}

	for _, s := range specification.MasterServers {
		add(s.Ip, s.PortSsh)
	}
	for _, s := range specification.VolumeServers {
		add(s.Ip, s.PortSsh)
	}
	for _, s := range specification.FilerServers {
		add(s.Ip, s.PortSsh)
	}
	for _, s := range specification.EnvoyServers {
		add(s.Ip, s.PortSsh)
	}

	prepare := m.prepareHostAddressFn
	if prepare == nil {
		prepare = m.PrepareHostAddress
	}
	for _, h := range order {
		info(fmt.Sprintf("Preparing host %s:%d", h.ip, h.sshPort))
		if err := prepare(h.ip, h.sshPort); err != nil {
			return fmt.Errorf("prepare host %s: %w", h.ip, err)
		}
	}
	return nil
}

// PrepareHostAddress connects to the given SSH address and runs PrepareHost.
func (m *Manager) PrepareHostAddress(ip string, sshPort int) error {
	if sshPort == 0 {
		sshPort = 22
	}
	return operator.ExecuteRemote(
		fmt.Sprintf("%s:%d", ip, sshPort),
		m.User,
		m.IdentityFile,
		m.sudoPass,
		func(op operator.CommandOperator) error {
			return m.PrepareHost(op)
		},
	)
}
