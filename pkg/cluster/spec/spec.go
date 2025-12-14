package spec

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

	// MasterServerSpec represents a master server configuration
	MasterServerSpec struct {
		Host               string   `yaml:"ip"`
		Port               int      `yaml:"port" default:"9333"`
		DataDir            string   `yaml:"dir,omitempty"`
		LogLevel           string   `yaml:"log_level,omitempty"`
		DefaultReplication string   `yaml:"default_replication,omitempty"`
		Peers              []string `yaml:"peers,omitempty"`
	}

	// VolumeServerSpec represents a volume server configuration
	VolumeServerSpec struct {
		Host       string             `yaml:"ip"`
		Port       int                `yaml:"port" default:"8080"`
		DataDir    string             `yaml:"dir,omitempty"`
		LogLevel   string             `yaml:"log_level,omitempty"`
		Masters    []string           `yaml:"masters,omitempty"`
		Folders    []VolumeFolderSpec `yaml:"folders,omitempty"`
		MaxVolumes int                `yaml:"max_volumes,omitempty"`
	}

	// FilerServerSpec represents a filer server configuration
	FilerServerSpec struct {
		Host               string   `yaml:"ip"`
		Port               int      `yaml:"port" default:"8888"`
		DataDir            string   `yaml:"dir,omitempty"`
		LogLevel           string   `yaml:"log_level,omitempty"`
		Masters            []string `yaml:"masters,omitempty"`
		Collection         string   `yaml:"collection,omitempty"`
		DefaultReplication string   `yaml:"default_replication,omitempty"`
		S3                 bool     `yaml:"s3,omitempty"`
		WebDAV             bool     `yaml:"webdav,omitempty"`
		S3Port             int      `yaml:"s3_port,omitempty" default:"8333"`
		WebDAVPort         int      `yaml:"webdav_port,omitempty" default:"7333"`
		// Note: IAM API is now embedded in S3 by default (on the same port as S3).
		// No configuration needed - IAM is automatically enabled when S3 is enabled.
	}

	// EnvoyServerSpec represents an envoy proxy configuration
	EnvoyServerSpec struct {
		Host     string   `yaml:"ip"`
		Port     int      `yaml:"port" default:"8000"`
		DataDir  string   `yaml:"dir,omitempty"`
		LogLevel string   `yaml:"log_level,omitempty"`
		Targets  []string `yaml:"targets,omitempty"`
	}

	// VolumeFolderSpec represents a volume folder configuration
	VolumeFolderSpec struct {
		Folder string `yaml:"folder"`
		Disk   string `yaml:"disk,omitempty"`
	}

	// ComponentSpec interface for all component specifications
	ComponentSpec interface {
		GetHost() string
		GetPort() int
		GetType() string
		GetDataDir() string
	}
)
