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

// InstallNodeExporter installs a pinned node_exporter release on the
// host reachable through op and registers it as a systemd service
// listening on :9100.
func InstallNodeExporter(op operator.CommandOperator) error {
	if op == nil {
		return fmt.Errorf("observability: nil CommandOperator")
	}

	arch, err := op.Output("uname -m")
	if err != nil {
		return fmt.Errorf("detect arch: %w", err)
	}
	goArch := "amd64"
	switch strings.TrimSpace(string(arch)) {
	case "aarch64", "arm64":
		goArch = "arm64"
	case "armv7l", "armv6l":
		goArch = "armv7"
	}

	tarball := fmt.Sprintf("node_exporter-%s.linux-%s.tar.gz", NodeExporterVersion, goArch)
	url := fmt.Sprintf("https://github.com/prometheus/node_exporter/releases/download/v%s/%s",
		NodeExporterVersion, tarball)

	script := strings.Join([]string{
		"set -e",
		"cd /tmp",
		fmt.Sprintf("curl -fsSL -o %s %q", tarball, url),
		fmt.Sprintf("tar -xzf %s", tarball),
		fmt.Sprintf("install -m 0755 node_exporter-%s.linux-%s/node_exporter /usr/local/bin/node_exporter",
			NodeExporterVersion, goArch),
		fmt.Sprintf("rm -rf %s node_exporter-%s.linux-%s", tarball, NodeExporterVersion, goArch),
		"id node_exporter >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin node_exporter",
	}, " && ")

	if err := op.Execute(script); err != nil {
		return fmt.Errorf("install node_exporter binary: %w", err)
	}

	unit := nodeExporterUnit()
	if err := op.Upload(strings.NewReader(unit),
		"/etc/systemd/system/node_exporter.service", "0644"); err != nil {
		return fmt.Errorf("upload systemd unit: %w", err)
	}

	if err := op.Execute("systemctl daemon-reload && systemctl enable node_exporter.service && systemctl restart node_exporter.service"); err != nil {
		return fmt.Errorf("enable node_exporter service: %w", err)
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
