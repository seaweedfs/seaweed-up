package spec

type (
	// GlobalOptions represents the global options for all groups in topology
	// specification in topology.yaml
	GlobalOptions struct {
		User       string `yaml:"user,omitempty" default:"seaweed"`
		SSHPort    int    `yaml:"ssh_port,omitempty" default:"22" validate:"ssh_port:editable"`
		TLSEnabled bool   `yaml:"enable_tls,omitempty"`
		ConfigDir  string `yaml:"conf_dir,omitempty" default:"/etc/seaweed"`
		DataDir    string `yaml:"data_dir,omitempty" default:"/opt/seaweed"`
		OS         string `yaml:"os,omitempty" default:"linux"`
		Arch       string `yaml:"arch,omitempty" default:"amd64"`
	}

	ServerConfigs struct {
		MasterServer map[string]interface{} `yaml:"master_server"`
		VolumeServer map[string]interface{} `yaml:"volume_server"`
		FilerServer  map[string]interface{} `yaml:"filer_server"`
	}

	Specification struct {
		GlobalOptions GlobalOptions       `yaml:"global,omitempty" validate:"global:editable"`
		ServerConfigs ServerConfigs       `yaml:"server_configs,omitempty" validate:"server_configs:ignore"`
		MasterServers []*MasterServerSpec `yaml:"master_servers"`
		VolumeServers []*VolumeServerSpec `yaml:"volume_servers"`
		FilerServers  []*FilerServerSpec  `yaml:"filer_servers"`
	}
)
