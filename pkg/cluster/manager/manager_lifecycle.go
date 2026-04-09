package manager

import (
	"fmt"
	"strings"
	"sync"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// LifecycleVerb represents a systemctl verb applied to seaweed units.
type LifecycleVerb string

const (
	LifecycleStart   LifecycleVerb = "start"
	LifecycleStop    LifecycleVerb = "stop"
	LifecycleRestart LifecycleVerb = "restart"
)

// hostEntry identifies a unique target host for lifecycle operations.
type hostEntry struct {
	ip      string
	sshPort int
}

// componentServiceNames returns the systemd unit names for the instances of a
// given component that live on a specific host IP. Naming mirrors the
// templates used by deploy: seaweed_<component><index>.service.
func componentServiceNames(s *spec.Specification, component, ip string) []string {
	var names []string
	switch component {
	case "master":
		for i, m := range s.MasterServers {
			if m.Ip == ip {
				names = append(names, fmt.Sprintf("seaweed_master%d.service", i))
			}
		}
	case "volume":
		for i, v := range s.VolumeServers {
			if v.Ip == ip {
				names = append(names, fmt.Sprintf("seaweed_volume%d.service", i))
			}
		}
	case "filer":
		for i, f := range s.FilerServers {
			if f.Ip == ip {
				names = append(names, fmt.Sprintf("seaweed_filer%d.service", i))
			}
		}
	}
	return names
}

// servicesForHost returns all seaweed_* systemd unit names for a host,
// optionally filtered by component ("" / "master" / "volume" / "filer").
func servicesForHost(s *spec.Specification, ip, component string) []string {
	var names []string
	comps := []string{"master", "volume", "filer"}
	if component != "" {
		comps = []string{component}
	}
	for _, c := range comps {
		names = append(names, componentServiceNames(s, c, ip)...)
	}
	return names
}

// uniqueHosts returns every unique host referenced by the specification,
// filtered to hosts relevant to the given component ("" for all).
func uniqueHosts(s *spec.Specification, component string) []hostEntry {
	seen := make(map[string]hostEntry)
	add := func(ip string, sshPort int) {
		if ip == "" {
			return
		}
		if _, ok := seen[ip]; !ok {
			seen[ip] = hostEntry{ip: ip, sshPort: sshPort}
		}
	}

	if component == "" || component == "master" {
		for _, m := range s.MasterServers {
			add(m.Ip, m.PortSsh)
		}
	}
	if component == "" || component == "volume" {
		for _, v := range s.VolumeServers {
			add(v.Ip, v.PortSsh)
		}
	}
	if component == "" || component == "filer" {
		for _, f := range s.FilerServers {
			add(f.Ip, f.PortSsh)
		}
	}

	out := make([]hostEntry, 0, len(seen))
	for _, h := range seen {
		out = append(out, h)
	}
	return out
}

// buildLifecycleCommand produces the remote shell command that applies a
// systemctl verb to a set of unit names. Errors from individual units are
// ignored so that missing units do not abort a whole host operation.
func buildLifecycleCommand(verb LifecycleVerb, services []string) string {
	if len(services) == 0 {
		return "true"
	}
	quoted := make([]string, 0, len(services))
	for _, svc := range services {
		quoted = append(quoted, fmt.Sprintf("'%s'", svc))
	}
	return fmt.Sprintf("systemctl %s %s || true", string(verb), strings.Join(quoted, " "))
}

// buildDestroyCommand produces the remote shell command that stops, disables
// and removes all seaweed_* units on a host. If removeData is true the
// configured data and config directories are also removed.
func buildDestroyCommand(services []string, dataDir, confDir string, removeData bool) string {
	var sb strings.Builder
	if len(services) > 0 {
		quoted := make([]string, 0, len(services))
		for _, svc := range services {
			quoted = append(quoted, fmt.Sprintf("'%s'", svc))
		}
		all := strings.Join(quoted, " ")
		sb.WriteString(fmt.Sprintf("systemctl stop %s || true; ", all))
		sb.WriteString(fmt.Sprintf("systemctl disable %s || true; ", all))
	}
	// Wildcard cleanup to catch any stale unit files we may not know about.
	sb.WriteString("rm -f /etc/systemd/system/seaweed_*.service; ")
	sb.WriteString("rm -f /etc/systemd/system/seaweed-*.service; ")
	sb.WriteString("systemctl daemon-reload || true")
	if removeData {
		if dataDir != "" {
			sb.WriteString(fmt.Sprintf("; rm -rf %s", dataDir))
		}
		if confDir != "" && confDir != dataDir {
			sb.WriteString(fmt.Sprintf("; rm -rf %s", confDir))
		}
	}
	return sb.String()
}

// applyLifecycle runs the given systemctl verb against all matching services
// on every host referenced by the specification, restricted by an optional
// component filter ("" for all).
func (m *Manager) applyLifecycle(specification *spec.Specification, verb LifecycleVerb, component string) error {
	m.prepare(specification)

	hosts := uniqueHosts(specification, component)
	if len(hosts) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, h := range hosts {
		h := h
		services := servicesForHost(specification, h.ip, component)
		cmd := buildLifecycleCommand(verb, services)
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := fmt.Sprintf("%s:%d", h.ip, h.sshPort)
			err := operator.ExecuteRemote(addr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
				return m.sudo(op, cmd)
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s on %s: %w", verb, h.ip, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// StartCluster runs `systemctl start` on all seaweed units across the cluster.
func (m *Manager) StartCluster(specification *spec.Specification, component string) error {
	return m.applyLifecycle(specification, LifecycleStart, component)
}

// StopCluster runs `systemctl stop` on all seaweed units across the cluster.
func (m *Manager) StopCluster(specification *spec.Specification, component string) error {
	return m.applyLifecycle(specification, LifecycleStop, component)
}

// RestartCluster runs `systemctl restart` on all seaweed units across the cluster.
func (m *Manager) RestartCluster(specification *spec.Specification, component string) error {
	return m.applyLifecycle(specification, LifecycleRestart, component)
}

// DestroyCluster stops, disables and removes all seaweed systemd units on
// every host. When removeData is true the data and config directories are
// also removed.
func (m *Manager) DestroyCluster(specification *spec.Specification, removeData bool) error {
	m.prepare(specification)

	hosts := uniqueHosts(specification, "")
	if len(hosts) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, h := range hosts {
		h := h
		services := servicesForHost(specification, h.ip, "")
		cmd := buildDestroyCommand(services, m.dataDir, m.confDir, removeData)
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := fmt.Sprintf("%s:%d", h.ip, h.sshPort)
			err := operator.ExecuteRemote(addr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
				return m.sudo(op, cmd)
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("destroy on %s: %w", h.ip, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
