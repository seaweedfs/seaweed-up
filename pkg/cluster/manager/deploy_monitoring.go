package manager

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/observability"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"golang.org/x/sync/errgroup"
)

// metricsPortBase is the first port assigned to a component's Prometheus
// metrics endpoint when one isn't set explicitly.
const metricsPortBase = 9324

// assignMetricsPorts gives every master/volume/filer without an explicit
// metrics_port a per-host-unique one (so two volume servers on the same
// host don't collide), starting at metricsPortBase. Explicitly-set ports
// are preserved and reserved. The same spec drives both the deploy
// (-metricsPort) and the rendered Prometheus scrape config, so they stay
// in sync. Only called when monitoring is enabled.
func assignMetricsPorts(s *spec.Specification) {
	if s == nil {
		return
	}
	used := map[string]map[int]bool{}
	mark := func(ip string, p int) {
		if p == 0 {
			return
		}
		if used[ip] == nil {
			used[ip] = map[int]bool{}
		}
		used[ip][p] = true
	}
	for _, m := range s.MasterServers {
		if m != nil {
			mark(m.Ip, m.MetricsPort)
		}
	}
	for _, v := range s.VolumeServers {
		if v != nil {
			mark(v.Ip, v.MetricsPort)
		}
	}
	for _, f := range s.FilerServers {
		if f != nil {
			mark(f.Ip, f.MetricsPort)
		}
	}

	next := map[string]int{}
	alloc := func(ip string) int {
		if used[ip] == nil {
			used[ip] = map[int]bool{}
		}
		p := next[ip]
		if p < metricsPortBase {
			p = metricsPortBase
		}
		for used[ip][p] {
			p++
		}
		used[ip][p] = true
		next[ip] = p + 1
		return p
	}
	for _, m := range s.MasterServers {
		if m != nil && m.MetricsPort == 0 {
			m.MetricsPort = alloc(m.Ip)
		}
	}
	for _, v := range s.VolumeServers {
		if v != nil && v.MetricsPort == 0 {
			v.MetricsPort = alloc(v.Ip)
		}
	}
	for _, f := range s.FilerServers {
		if f != nil && f.MetricsPort == 0 {
			f.MetricsPort = alloc(f.Ip)
		}
	}
}

type monHost struct {
	ip   string
	port int
}

// monitoringNodeHosts returns the deduplicated (ip, ssh-port) set of hosts
// that should run node_exporter — every host carrying a master, volume, or
// filer.
func monitoringNodeHosts(s *spec.Specification) []monHost {
	seen := map[string]struct{}{}
	var out []monHost
	add := func(ip string, port int) {
		if ip == "" {
			return
		}
		if port == 0 {
			port = 22
		}
		key := fmt.Sprintf("%s:%d", ip, port)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, monHost{ip: ip, port: port})
	}
	for _, m := range s.MasterServers {
		if m != nil {
			add(m.Ip, m.PortSsh)
		}
	}
	for _, v := range s.VolumeServers {
		if v != nil {
			add(v.Ip, v.PortSsh)
		}
	}
	for _, f := range s.FilerServers {
		if f != nil {
			add(f.Ip, f.PortSsh)
		}
	}
	return out
}

// DeployMonitoring installs node_exporter on every host (unless disabled)
// and the Prometheus + Grafana stack on the monitoring host, with the
// scrape config rendered from the spec and the bundled SeaweedFS dashboard
// provisioned.
func (m *Manager) DeployMonitoring(s *spec.Specification) error {
	mon := s.Monitoring
	if mon == nil {
		return nil
	}

	if mon.InstallNodeExporter() {
		hosts := monitoringNodeHosts(s)
		var eg errgroup.Group
		eg.SetLimit(8)
		var (
			mu   sync.Mutex
			errs []error
		)
		for _, h := range hosts {
			h := h
			eg.Go(func() error {
				info(fmt.Sprintf("Installing node_exporter on %s...", h.ip))
				addr := fmt.Sprintf("%s:%d", h.ip, h.port)
				err := operator.ExecuteRemote(addr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
					return observability.InstallNodeExporter(op, m.User, m.sudoPass)
				})
				if err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("node_exporter on %s: %w", h.ip, err))
					mu.Unlock()
				}
				return nil
			})
		}
		_ = eg.Wait()
		if len(errs) > 0 {
			return errs[0]
		}
		info(fmt.Sprintf("node_exporter installed on %d host(s)", len(hosts)))
	}

	monAddr := fmt.Sprintf("%s:%d", mon.Host, utils.NvlInt(mon.PortSsh, m.SshPort, 22))

	var promCfg bytes.Buffer
	promCfg.WriteString("global:\n  scrape_interval: 15s\n  evaluation_interval: 15s\n\n")
	promCfg.WriteString(observability.RenderPromConfig(s))

	info("Installing Prometheus on " + mon.Host)
	if err := operator.ExecuteRemote(monAddr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		return observability.InstallPrometheus(op, m.User, m.sudoPass, observability.PrometheusOptions{
			ConfigYAML: promCfg.String(),
			Bind:       mon.EffectiveBind(),
			Port:       mon.EffectivePrometheusPort(),
			Retention:  mon.EffectiveRetention(),
		})
	}); err != nil {
		return fmt.Errorf("install prometheus on %s: %w", mon.Host, err)
	}

	info("Installing Grafana on " + mon.Host)
	if err := operator.ExecuteRemote(monAddr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		return observability.InstallGrafana(op, m.User, m.sudoPass, observability.GrafanaOptions{
			Bind:          mon.EffectiveBind(),
			Port:          mon.EffectiveGrafanaPort(),
			AdminUser:     mon.EffectiveGrafanaAdminUser(),
			AdminPassword: mon.GrafanaAdminPassword,
			PrometheusURL: fmt.Sprintf("http://127.0.0.1:%d", mon.EffectivePrometheusPort()),
			ClusterName:   s.Name,
		})
	}); err != nil {
		return fmt.Errorf("install grafana on %s: %w", mon.Host, err)
	}

	info(fmt.Sprintf("Monitoring deployed: Grafana on %s:%d (bind %s), Prometheus :%d",
		mon.Host, mon.EffectiveGrafanaPort(), mon.EffectiveBind(), mon.EffectivePrometheusPort()))
	return nil
}
