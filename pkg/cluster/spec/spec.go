package spec

import (
	"fmt"
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
	}
)

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

	return nil
}
