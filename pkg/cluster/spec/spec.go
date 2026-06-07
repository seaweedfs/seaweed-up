package spec

import (
	"fmt"
	"regexp"
	"strings"
)

type (
	// GlobalOptions represents the global options for all groups in topology
	// specification in topology.yaml
	GlobalOptions struct {
		TLSEnabled        bool         `yaml:"enable_tls,omitempty"`
		ConfigDir         string       `yaml:"dir.conf,omitempty" default:"/etc/seaweed"`
		DataDir           string       `yaml:"dir.data,omitempty" default:"/opt/seaweed"`
		OS                string       `yaml:"os,omitempty" default:"linux"`
		VolumeSizeLimitMB int          `yaml:"volumeSizeLimitMB" default:"5000"`
		Replication       string       `yaml:"replication" default:"000"`
		Bastion           *BastionSpec `yaml:"bastion,omitempty"`
		// SSHHostKeyCheck controls verification of remote SSH host keys
		// for every connection (direct and through the bastion). One of:
		//   ""/"ignore"  - no verification (default; backward compatible)
		//   "accept-new" - learn unknown hosts (TOFU), reject changed keys
		//   "strict"     - host must already be in ~/.ssh/known_hosts
		SSHHostKeyCheck string `yaml:"ssh_host_key_check,omitempty"`
	}

	// BastionSpec configures an SSH jump host that seaweed-up tunnels
	// every node connection through. Use it when the cluster nodes live
	// on a private network reachable only via a public bastion (the
	// `ssh bastion` then `ssh 10.0.0.x` pattern). All fields except host
	// are optional: user defaults to the current OS user, port to 22,
	// and auth falls back to the ssh agent when identity and password
	// are both unset. Prefer identity/agent auth: like the other
	// credentials in this spec (e.g. admin_password, filer DB password),
	// Password is persisted in plaintext when the cluster state is saved.
	BastionSpec struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port,omitempty"`
		User     string `yaml:"user,omitempty"`
		Identity string `yaml:"identity,omitempty"`
		Password string `yaml:"password,omitempty"`
	}

	ServerConfigs struct {
		MasterServer map[string]interface{} `yaml:"master_server"`
		VolumeServer map[string]interface{} `yaml:"volume_server"`
		FilerServer  map[string]interface{} `yaml:"filer_server"`
	}

	Specification struct {
		Name          string        `yaml:"cluster_name,omitempty"`
		GlobalOptions GlobalOptions `yaml:"global,omitempty" validate:"global:editable"`
		ServerConfigs ServerConfigs `yaml:"server_configs,omitempty" validate:"server_configs:ignore"`
		// master_servers is required (Validate refuses a spec without
		// one); everything else is optional. omitempty on the optional
		// sections keeps generated cluster.yaml files tidy — no
		// `s3_servers: []` lines for clusters that don't run an S3
		// gateway.
		MasterServers []*MasterServerSpec `yaml:"master_servers"`
		VolumeServers []*VolumeServerSpec `yaml:"volume_servers,omitempty"`
		FilerServers  []*FilerServerSpec  `yaml:"filer_servers,omitempty"`
		S3Servers     []*S3ServerSpec     `yaml:"s3_servers,omitempty"`
		SftpServers   []*SftpServerSpec   `yaml:"sftp_servers,omitempty"`
		AdminServers  []*AdminServerSpec  `yaml:"admin_servers,omitempty"`
		EnvoyServers  []*EnvoyServerSpec  `yaml:"envoy_servers,omitempty"`
		WorkerServers []*WorkerServerSpec `yaml:"worker_servers,omitempty"`
		// Monitoring, when set, makes seaweed-up deploy a Prometheus +
		// Grafana stack (and node_exporter on every host) as part of the
		// cluster, with the bundled SeaweedFS dashboard pre-loaded.
		Monitoring *MonitoringSpec `yaml:"monitoring,omitempty"`
	}

	// MonitoringSpec configures the bundled observability stack. Only Host
	// is required; everything else has a sensible default (see the
	// Effective* helpers). By default Prometheus and Grafana bind to
	// 127.0.0.1 on the monitoring host, so reach Grafana over an SSH
	// tunnel; set bind: 0.0.0.0 to expose them (mind the same cleartext /
	// firewall caveats as any public service).
	MonitoringSpec struct {
		Host                 string `yaml:"host"`
		PortSsh              int    `yaml:"port.ssh,omitempty"`
		Bind                 string `yaml:"bind,omitempty"`
		PrometheusPort       int    `yaml:"prometheus_port,omitempty"`
		GrafanaPort          int    `yaml:"grafana_port,omitempty"`
		GrafanaAdminUser     string `yaml:"grafana_admin_user,omitempty"`
		GrafanaAdminPassword string `yaml:"grafana_admin_password,omitempty"`
		Retention            string `yaml:"retention,omitempty"`
		// NodeExporter controls whether node_exporter is installed on
		// every master/volume/filer host (pointer so unset defaults to true).
		NodeExporter *bool `yaml:"node_exporter,omitempty"`
	}
)

// Default ports / values for the monitoring stack.
const (
	DefaultPrometheusPort   = 9090
	DefaultGrafanaPort      = 3000
	DefaultMonitoringBind   = "127.0.0.1"
	DefaultGrafanaAdminUser = "admin"
	DefaultPromRetention    = "15d"
)

// EffectiveBind returns the configured bind address or the default.
func (m *MonitoringSpec) EffectiveBind() string {
	if strings.TrimSpace(m.Bind) == "" {
		return DefaultMonitoringBind
	}
	return m.Bind
}

// EffectivePrometheusPort returns the configured Prometheus port or the default.
func (m *MonitoringSpec) EffectivePrometheusPort() int {
	if m.PrometheusPort == 0 {
		return DefaultPrometheusPort
	}
	return m.PrometheusPort
}

// EffectiveGrafanaPort returns the configured Grafana port or the default.
func (m *MonitoringSpec) EffectiveGrafanaPort() int {
	if m.GrafanaPort == 0 {
		return DefaultGrafanaPort
	}
	return m.GrafanaPort
}

// EffectiveGrafanaAdminUser returns the configured admin user or the default.
func (m *MonitoringSpec) EffectiveGrafanaAdminUser() string {
	if strings.TrimSpace(m.GrafanaAdminUser) == "" {
		return DefaultGrafanaAdminUser
	}
	return m.GrafanaAdminUser
}

// EffectiveRetention returns the configured Prometheus retention or the default.
func (m *MonitoringSpec) EffectiveRetention() string {
	if strings.TrimSpace(m.Retention) == "" {
		return DefaultPromRetention
	}
	return m.Retention
}

// InstallNodeExporter reports whether node_exporter should be installed
// on every host (default true when unset).
func (m *MonitoringSpec) InstallNodeExporter() bool {
	return m.NodeExporter == nil || *m.NodeExporter
}

// promDurationRe matches a Prometheus duration as accepted by
// --storage.tsdb.retention.time (model.ParseDuration): number+unit tokens in
// descending unit order (y, w, d, h, m, s, ms). Mirrors Prometheus's own regex
// so a bad monitoring.retention is caught here, not at Prometheus startup.
var promDurationRe = regexp.MustCompile(`^(?:[0-9]+y)?(?:[0-9]+w)?(?:[0-9]+d)?(?:[0-9]+h)?(?:[0-9]+m)?(?:[0-9]+s)?(?:[0-9]+ms)?$`)

// Validate validates the Specification and returns an error if invalid
func (s *Specification) Validate() error {
	if len(s.MasterServers) == 0 {
		return fmt.Errorf("at least one master server is required")
	}

	// Name is optional but validated if provided
	// The Name can be set from command line args if not in config

	// A configured bastion must at least name a host, and (if given) a
	// sane port — otherwise the misconfiguration only surfaces later as
	// an opaque SSH dial error. Port 0 is allowed: it means "unset", and
	// the operator defaults it to 22.
	if b := s.GlobalOptions.Bastion; b != nil {
		if strings.TrimSpace(b.Host) == "" {
			return fmt.Errorf("global.bastion.host is required when a bastion is configured")
		}
		if b.Port < 0 || b.Port > 65535 {
			return fmt.Errorf("global.bastion.port %d is out of range (0-65535)", b.Port)
		}
	}

	// Keep these values in sync with operator.ValidHostKeyPolicy; checked
	// inline here to avoid the spec package importing operator.
	switch s.GlobalOptions.SSHHostKeyCheck {
	case "", "ignore", "accept-new", "strict":
	default:
		return fmt.Errorf("global.ssh_host_key_check %q is invalid (want ignore, accept-new, or strict)",
			s.GlobalOptions.SSHHostKeyCheck)
	}

	if mon := s.Monitoring; mon != nil {
		if strings.TrimSpace(mon.Host) == "" {
			return fmt.Errorf("monitoring.host is required when monitoring is configured")
		}
		// An empty Grafana admin password is a real security risk and leaves
		// grafana.ini with a blank credential; require it explicitly.
		if strings.TrimSpace(mon.GrafanaAdminPassword) == "" {
			return fmt.Errorf("monitoring.grafana_admin_password is required when monitoring is configured")
		}
		for label, p := range map[string]int{
			"monitoring.port.ssh":        mon.PortSsh,
			"monitoring.prometheus_port": mon.PrometheusPort,
			"monitoring.grafana_port":    mon.GrafanaPort,
		} {
			if p < 0 || p > 65535 {
				return fmt.Errorf("%s %d is out of range (0-65535)", label, p)
			}
		}
		if r := strings.TrimSpace(mon.Retention); r != "" && !promDurationRe.MatchString(r) {
			return fmt.Errorf("monitoring.retention %q is not a valid Prometheus duration (e.g. 15d, 6h, 1y)", mon.Retention)
		}
	}

	return nil
}
