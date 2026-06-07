package observability

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// prepareDashboard returns the bundled dashboard JSON ready for Grafana
// file provisioning: "id" removed (Grafana assigns one) and, when
// clusterName is set, the title suffixed so multiple clusters coexist.
// Unlike buildDashboardPayload (the API path), this returns the bare
// dashboard model, which is what the file provider expects. Falls back to
// the raw bytes if the JSON can't be parsed.
func prepareDashboard(raw []byte, clusterName string) []byte {
	var dash map[string]interface{}
	if err := json.Unmarshal(raw, &dash); err != nil {
		return raw
	}
	delete(dash, "id")
	if clusterName != "" {
		if title, ok := dash["title"].(string); ok && title != "" {
			dash["title"] = title + " - " + clusterName
		}
	}
	out, err := json.Marshal(dash)
	if err != nil {
		return raw
	}
	return out
}

// Pinned versions for the bundled monitoring stack. node_exporter is
// pinned in observability.go (NodeExporterVersion).
const (
	PrometheusVersion = "2.55.1"
	GrafanaVersion    = "11.6.6"
)

// PrometheusOptions configures InstallPrometheus.
type PrometheusOptions struct {
	// ConfigYAML is the full prometheus.yml content (global + scrape_configs).
	ConfigYAML string
	// Bind is the listen address (e.g. 127.0.0.1); Port the listen port.
	Bind string
	Port int
	// Retention is the tsdb retention window, e.g. "15d".
	Retention string
}

// GrafanaOptions configures InstallGrafana.
type GrafanaOptions struct {
	Bind          string
	Port          int
	AdminUser     string
	AdminPassword string
	// PrometheusURL is the datasource URL provisioned in Grafana.
	PrometheusURL string
	// ClusterName, when set, is appended to the dashboard title.
	ClusterName string
}

// shellQuote single-quotes s for safe embedding in a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runScript runs script on the remote host, elevating to root the same
// way pkg/cluster/tls.runInstallScript does: direct when the SSH user is
// root, `sudo -n` when a non-root user has passwordless sudo, and
// `sudo -S` when a sudo password is supplied. The script is base64-encoded
// so its contents (heredocs, embedded files) survive shell quoting.
func runScript(op operator.CommandOperator, sshUser, sudoPass, script string) error {
	enc := base64.StdEncoding.EncodeToString([]byte(script))
	var cmd string
	switch {
	case sshUser == "root":
		cmd = fmt.Sprintf("echo %s | base64 -d | sh", enc)
	case sudoPass == "":
		cmd = fmt.Sprintf("echo %s | base64 -d | sudo -n sh", enc)
	default:
		// printf (not echo) emits the password so one starting with '-'
		// isn't swallowed as an echo flag.
		cmd = fmt.Sprintf("(printf '%%s\\n' %s; echo %s | base64 -d) | sudo -S -p '' sh", shellQuote(sudoPass), enc)
	}
	return op.Execute(cmd)
}

// writeFileCmd returns a shell snippet that writes content to path with
// the given mode, embedding content as base64 so arbitrary bytes are safe.
func writeFileCmd(path, content, mode string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	return fmt.Sprintf("echo %s | base64 -d > %s\nchmod %s %s", enc, shellQuote(path), mode, shellQuote(path))
}

// PrometheusInstallScript renders the shell script that installs and
// starts Prometheus. Exposed (and pure) so it can be unit-tested.
func PrometheusInstallScript(goArch string, opts PrometheusOptions) string {
	bind := opts.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	port := opts.Port
	if port == 0 {
		port = 9090
	}
	retention := opts.Retention
	if retention == "" {
		retention = "15d"
	}
	tb := fmt.Sprintf("prometheus-%s.linux-%s", PrometheusVersion, goArch)
	url := fmt.Sprintf("https://github.com/prometheus/prometheus/releases/download/v%s/%s.tar.gz",
		PrometheusVersion, tb)

	unit := fmt.Sprintf(`[Unit]
Description=Prometheus
After=network-online.target
Wants=network-online.target

[Service]
User=prometheus
Group=prometheus
ExecStart=/usr/local/bin/prometheus --config.file=/etc/prometheus/prometheus.yml --storage.tsdb.path=/var/lib/prometheus --storage.tsdb.retention.time=%s --web.listen-address=%s:%d
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, retention, bind, port)

	return strings.Join([]string{
		"set -e",
		"cd /tmp",
		fmt.Sprintf("if [ ! -x /usr/local/bin/prometheus ]; then curl -fsSL -o %s.tar.gz %q && tar -xzf %s.tar.gz && install -m0755 %s/prometheus /usr/local/bin/prometheus && install -m0755 %s/promtool /usr/local/bin/promtool && rm -rf %s.tar.gz %s; fi", tb, url, tb, tb, tb, tb, tb),
		"id prometheus >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin prometheus",
		"mkdir -p /etc/prometheus /var/lib/prometheus",
		writeFileCmd("/etc/prometheus/prometheus.yml", opts.ConfigYAML, "0644"),
		"chown -R prometheus:prometheus /etc/prometheus /var/lib/prometheus",
		"/usr/local/bin/promtool check config /etc/prometheus/prometheus.yml",
		writeFileCmd("/etc/systemd/system/prometheus.service", unit, "0644"),
		"systemctl daemon-reload",
		"systemctl enable prometheus.service >/dev/null 2>&1",
		"systemctl restart prometheus.service",
	}, "\n")
}

// InstallPrometheus installs and starts Prometheus on the host behind op.
func InstallPrometheus(op operator.CommandOperator, sshUser, sudoPass string, opts PrometheusOptions) error {
	if op == nil {
		return fmt.Errorf("observability: nil CommandOperator")
	}
	goArch, err := remoteGoArch(op)
	if err != nil {
		return err
	}
	if err := runScript(op, sshUser, sudoPass, PrometheusInstallScript(goArch, opts)); err != nil {
		return fmt.Errorf("install prometheus: %w", err)
	}
	return nil
}

// GrafanaInstallScript renders the shell script that installs and starts
// Grafana with the Prometheus datasource and bundled dashboard
// provisioned. Pure, for unit tests.
func GrafanaInstallScript(goArch string, dashboard []byte, opts GrafanaOptions) string {
	bind := opts.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	port := opts.Port
	if port == 0 {
		port = 3000
	}
	user := opts.AdminUser
	if user == "" {
		user = "admin"
	}
	promURL := opts.PrometheusURL
	if promURL == "" {
		promURL = "http://127.0.0.1:9090"
	}
	// The official Grafana release tarball extracts to grafana-<version>
	// (no leading "v"), e.g. grafana-11.6.6.
	dir := fmt.Sprintf("grafana-%s", GrafanaVersion)
	url := fmt.Sprintf("https://dl.grafana.com/oss/release/grafana-%s.linux-%s.tar.gz",
		GrafanaVersion, goArch)

	ini := fmt.Sprintf(`[server]
http_addr = %s
http_port = %d
[security]
admin_user = %s
admin_password = %s
[paths]
data = /var/lib/grafana
logs = /var/log/grafana
provisioning = /etc/grafana/provisioning
[analytics]
reporting_enabled = false
check_for_updates = false
`, bind, port, user, opts.AdminPassword)

	datasource := `apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    uid: seaweedprom
    url: ` + promURL + `
    isDefault: true
`
	provider := `apiVersion: 1
providers:
  - name: seaweedfs
    type: file
    allowUiUpdates: true
    options:
      path: /var/lib/grafana/dashboards
      foldersFromFilesStructure: false
`
	unit := `[Unit]
Description=Grafana
After=network-online.target
Wants=network-online.target

[Service]
User=grafana
Group=grafana
ExecStart=/opt/grafana/bin/grafana server --homepath=/opt/grafana --config=/etc/grafana/grafana.ini
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

	return strings.Join([]string{
		"set -e",
		"cd /tmp",
		fmt.Sprintf("if [ ! -x /opt/grafana/bin/grafana ]; then curl -fsSL -o grafana.tar.gz %q && tar -xzf grafana.tar.gz && rm -rf /opt/grafana && mv %s /opt/grafana && rm -f grafana.tar.gz; fi", url, dir),
		"id grafana >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin grafana",
		"mkdir -p /etc/grafana/provisioning/datasources /etc/grafana/provisioning/dashboards /var/lib/grafana/dashboards /var/log/grafana",
		writeFileCmd("/etc/grafana/grafana.ini", ini, "0640"),
		writeFileCmd("/etc/grafana/provisioning/datasources/prometheus.yml", datasource, "0644"),
		writeFileCmd("/etc/grafana/provisioning/dashboards/seaweedfs.yml", provider, "0644"),
		writeFileCmd("/var/lib/grafana/dashboards/seaweedfs.json", string(prepareDashboard(dashboard, opts.ClusterName)), "0644"),
		"chown -R grafana:grafana /etc/grafana /var/lib/grafana /var/log/grafana",
		writeFileCmd("/etc/systemd/system/grafana.service", unit, "0644"),
		"systemctl daemon-reload",
		"systemctl enable grafana.service >/dev/null 2>&1",
		"systemctl restart grafana.service",
	}, "\n")
}

// InstallGrafana installs and starts Grafana on the host behind op.
func InstallGrafana(op operator.CommandOperator, sshUser, sudoPass string, opts GrafanaOptions) error {
	if op == nil {
		return fmt.Errorf("observability: nil CommandOperator")
	}
	goArch, err := remoteGoArch(op)
	if err != nil {
		return err
	}
	if err := runScript(op, sshUser, sudoPass, GrafanaInstallScript(goArch, DashboardJSON(), opts)); err != nil {
		return fmt.Errorf("install grafana: %w", err)
	}
	return nil
}

// remoteGoArch maps the remote `uname -m` to a Go/release arch token.
func remoteGoArch(op operator.CommandOperator) (string, error) {
	arch, err := op.Output("uname -m")
	if err != nil {
		return "", fmt.Errorf("detect arch: %w", err)
	}
	switch strings.TrimSpace(string(arch)) {
	case "aarch64", "arm64":
		return "arm64", nil
	case "armv7l", "armv6l":
		// node_exporter, Prometheus and Grafana all publish linux-armv7
		// tarballs; map 32-bit ARM there rather than mislabelling amd64.
		return "armv7", nil
	default:
		return "amd64", nil
	}
}
