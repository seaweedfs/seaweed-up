package spec

import "fmt"

type (
	// GlobalOptions represents the global options for all groups in topology
	// specification in topology.yaml
	GlobalOptions struct {
		TLSEnabled        bool   `yaml:"enable_tls,omitempty"`
		ConfigDir         string `yaml:"dir.conf,omitempty" default:"/etc/seaweed"`
		DataDir           string `yaml:"dir.data,omitempty" default:"/opt/seaweed"`
		OS                string `yaml:"os,omitempty" default:"linux"`
		VolumeSizeLimitMB int    `yaml:"volumeSizeLimitMB" default:"5000"`
		Replication       string `yaml:"replication" default:"000"`
	}

	ServerConfigs struct {
		MasterServer map[string]interface{} `yaml:"master_server"`
		VolumeServer map[string]interface{} `yaml:"volume_server"`
		FilerServer  map[string]interface{} `yaml:"filer_server"`
	}

	Specification struct {
		Name          string              `yaml:"cluster_name,omitempty"`
		GlobalOptions GlobalOptions       `yaml:"global,omitempty" validate:"global:editable"`
		ServerConfigs ServerConfigs       `yaml:"server_configs,omitempty" validate:"server_configs:ignore"`
		MasterServers []*MasterServerSpec `yaml:"master_servers"`
		VolumeServers []*VolumeServerSpec `yaml:"volume_servers"`
		FilerServers  []*FilerServerSpec  `yaml:"filer_servers"`
		EnvoyServers  []*EnvoyServerSpec  `yaml:"envoy_servers"`
	}
)

// Validate validates the Specification and returns an error if invalid
func (s *Specification) Validate() error {
	if len(s.MasterServers) == 0 {
		return fmt.Errorf("at least one master server is required")
	}

	// Name is optional but validated if provided
	// The Name can be set from command line args if not in config

	return nil
}
