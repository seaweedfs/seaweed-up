package manager

import (
	"bytes"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/observability"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"golang.org/x/sync/errgroup"
)

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
		for _, h := range hosts {
			h := h
			eg.Go(func() error {
				m.info(fmt.Sprintf("Installing node_exporter on %s...", h.ip))
				addr := fmt.Sprintf("%s:%d", h.ip, h.port)
				if err := operator.ExecuteRemote(addr, m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
					return observability.InstallNodeExporter(op, m.User, m.sudoPass)
				}); err != nil {
					return fmt.Errorf("node_exporter on %s: %w", h.ip, err)
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}
		m.info(fmt.Sprintf("node_exporter installed on %d host(s)", len(hosts)))
	}

	monAddr := fmt.Sprintf("%s:%d", mon.Host, utils.NvlInt(mon.PortSsh, m.SshPort, 22))

	var promCfg bytes.Buffer
	promCfg.WriteString("global:\n  scrape_interval: 15s\n  evaluation_interval: 15s\n\n")
	promCfg.WriteString(observability.RenderPromConfig(s))

	m.info("Installing Prometheus on " + mon.Host)
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

	m.info("Installing Grafana on " + mon.Host)
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

	m.info(fmt.Sprintf("Monitoring deployed: Grafana on %s:%d (bind %s), Prometheus :%d",
		mon.Host, mon.EffectiveGrafanaPort(), mon.EffectiveBind(), mon.EffectivePrometheusPort()))
	return nil
}
