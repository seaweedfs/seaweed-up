// Package observability provides helpers for bootstrapping metrics
// collection against a SeaweedFS cluster deployed by seaweed-up.
//
// It can install node_exporter on cluster hosts over SSH, render a
// Prometheus scrape-config snippet for the cluster's SeaweedFS
// components, and publish a bundled Grafana dashboard.
package observability

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// NodeExporterVersion is the pinned node_exporter release installed by
// InstallNodeExporter.
const NodeExporterVersion = "1.8.2"

// NodeExporterPort is the TCP port node_exporter listens on.
const NodeExporterPort = 9100

// Default metrics ports for SeaweedFS components when the spec does
// not explicitly set one.
const (
	DefaultMasterMetricsPort = 9324
	DefaultVolumeMetricsPort = 9325
	DefaultFilerMetricsPort  = 9326
)

// dashboardJSON is the bundled Grafana dashboard shipped with
// seaweed-up. It is exposed for tests and callers that need to push it
// to Grafana.
//
//go:embed dashboard.json
var dashboardJSON []byte

// DashboardJSON returns a copy of the bundled Grafana dashboard JSON.
func DashboardJSON() []byte {
	out := make([]byte, len(dashboardJSON))
	copy(out, dashboardJSON)
	return out
}

// NodeExporterInstallScript renders the shell script that installs and
// starts node_exporter. Pure, for unit tests.
func NodeExporterInstallScript(goArch string) string {
	tarball := fmt.Sprintf("node_exporter-%s.linux-%s.tar.gz", NodeExporterVersion, goArch)
	dir := fmt.Sprintf("node_exporter-%s.linux-%s", NodeExporterVersion, goArch)
	url := fmt.Sprintf("https://github.com/prometheus/node_exporter/releases/download/v%s/%s",
		NodeExporterVersion, tarball)
	return strings.Join([]string{
		"set -e",
		"cd /tmp",
		fmt.Sprintf("if [ ! -x /usr/local/bin/node_exporter ]; then curl -fsSL -o %s %q && tar -xzf %s && install -m0755 %s/node_exporter /usr/local/bin/node_exporter && rm -rf %s %s; fi",
			tarball, url, tarball, dir, tarball, dir),
		"id node_exporter >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin node_exporter",
		writeFileCmd("/etc/systemd/system/node_exporter.service", nodeExporterUnit(), "0644"),
		"systemctl daemon-reload",
		"systemctl enable node_exporter.service >/dev/null 2>&1",
		"systemctl restart node_exporter.service",
	}, "\n")
}

// InstallNodeExporter installs a pinned node_exporter release on the host
// reachable through op and registers it as a systemd service listening on
// :9100. It elevates via sudo for non-root SSH users (previously it ran
// the privileged steps bare, which failed for a normal login user).
func InstallNodeExporter(op operator.CommandOperator, sshUser, sudoPass string) error {
	if op == nil {
		return fmt.Errorf("observability: nil CommandOperator")
	}
	goArch, err := remoteGoArch(op)
	if err != nil {
		return err
	}
	if err := runScript(op, sshUser, sudoPass, NodeExporterInstallScript(goArch)); err != nil {
		return fmt.Errorf("install node_exporter: %w", err)
	}
	return nil
}

func nodeExporterUnit() string {
	return fmt.Sprintf(`[Unit]
Description=Prometheus Node Exporter
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=node_exporter
Group=node_exporter
ExecStart=/usr/local/bin/node_exporter --web.listen-address=:%d
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, NodeExporterPort)
}

// RenderPromConfig returns a YAML snippet with Prometheus scrape_configs
// for the SeaweedFS components described by spec, together with a
// node_exporter job covering every unique host.
func RenderPromConfig(s *spec.Specification) string {
	if s == nil {
		return ""
	}

	name := s.Name
	if name == "" {
		name = "seaweedfs"
	}

	var b strings.Builder
	b.WriteString("scrape_configs:\n")

	writeJob := func(job string, targets []string) {
		if len(targets) == 0 {
			return
		}
		sort.Strings(targets)
		fmt.Fprintf(&b, "  - job_name: %q\n", job)
		b.WriteString("    metrics_path: /metrics\n")
		b.WriteString("    static_configs:\n")
		b.WriteString("      - targets:\n")
		for _, t := range targets {
			fmt.Fprintf(&b, "          - %q\n", t)
		}
		fmt.Fprintf(&b, "        labels:\n          cluster: %q\n", name)
	}

	var masters, volumes, filers []string
	hosts := map[string]struct{}{}

	for _, m := range s.MasterServers {
		port := m.MetricsPort
		if port == 0 {
			port = DefaultMasterMetricsPort
		}
		masters = append(masters, fmt.Sprintf("%s:%d", m.Ip, port))
		hosts[m.Ip] = struct{}{}
	}
	for _, v := range s.VolumeServers {
		port := v.MetricsPort
		if port == 0 {
			port = DefaultVolumeMetricsPort
		}
		volumes = append(volumes, fmt.Sprintf("%s:%d", v.Ip, port))
		hosts[v.Ip] = struct{}{}
	}
	for _, f := range s.FilerServers {
		port := f.MetricsPort
		if port == 0 {
			port = DefaultFilerMetricsPort
		}
		filers = append(filers, fmt.Sprintf("%s:%d", f.Ip, port))
		hosts[f.Ip] = struct{}{}
	}

	writeJob(name+"-master", masters)
	writeJob(name+"-volume", volumes)
	writeJob(name+"-filer", filers)

	var nodeTargets []string
	for h := range hosts {
		nodeTargets = append(nodeTargets, fmt.Sprintf("%s:%d", h, NodeExporterPort))
	}
	writeJob(name+"-node-exporter", nodeTargets)

	return b.String()
}
