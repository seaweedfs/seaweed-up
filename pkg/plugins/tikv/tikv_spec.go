// Package tikv defines TiKV cluster specifications
package tikv

import (
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// TiKVClusterSpec defines the specification for a TiKV cluster
type TiKVClusterSpec struct {
	spec.Specification `yaml:",inline"`
	
	// TiKV specific configuration
	PD     []PDSpec     `yaml:"pd"`
	TiKV   []TiKVSpec   `yaml:"tikv"`
	TiDB   []TiDBSpec   `yaml:"tidb,omitempty"`
	Global TiKVGlobal   `yaml:"global"`
}

// PDSpec defines Placement Driver configuration
type PDSpec struct {
	Host         string            `yaml:"host"`
	ClientPort   int               `yaml:"client_port"` // Default: 2379
	PeerPort     int               `yaml:"peer_port"`   // Default: 2380
	DataDir      string            `yaml:"data_dir"`
	LogDir       string            `yaml:"log_dir"`
	SSHPort      int               `yaml:"ssh_port"`
	User         string            `yaml:"user"`
	Labels       map[string]string `yaml:"labels,omitempty"`
}

// TiKVSpec defines TiKV node configuration  
type TiKVSpec struct {
	Host       string            `yaml:"host"`
	Port       int               `yaml:"port"`        // Default: 20160
	StatusPort int               `yaml:"status_port"` // Default: 20180
	DataDir    string            `yaml:"data_dir"`
	LogDir     string            `yaml:"log_dir"`
	SSHPort    int               `yaml:"ssh_port"`
	User       string            `yaml:"user"`
	Labels     map[string]string `yaml:"labels,omitempty"`
	
	// Storage configuration
	Storage TiKVStorage `yaml:"storage"`
}

// TiDBSpec defines TiDB SQL layer configuration (optional)
type TiDBSpec struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`        // Default: 4000
	StatusPort int    `yaml:"status_port"` // Default: 10080
	LogDir     string `yaml:"log_dir"`
	SSHPort    int    `yaml:"ssh_port"`
	User       string `yaml:"user"`
}

// TiKVStorage defines storage configuration for TiKV nodes
type TiKVStorage struct {
	Engine      string `yaml:"engine"`       // "rocksdb" or "titan"
	Capacity    string `yaml:"capacity"`     // e.g., "500GB"
	ReserveSpace string `yaml:"reserve_space"` // e.g., "5GB"
	
	// RocksDB configuration
	RocksDB TiKVRocksDB `yaml:"rocksdb,omitempty"`
}

// TiKVRocksDB defines RocksDB specific configuration
type TiKVRocksDB struct {
	MaxOpenFiles        int    `yaml:"max_open_files"`
	MaxBackgroundJobs   int    `yaml:"max_background_jobs"`
	WriteBufferSize     string `yaml:"write_buffer_size"`
	MaxWriteBufferNumber int   `yaml:"max_write_buffer_number"`
}

// TiKVGlobal defines global TiKV cluster settings
type TiKVGlobal struct {
	Version     string `yaml:"version"`      // TiKV version
	User        string `yaml:"user"`         // Default service user
	Group       string `yaml:"group"`        // Default service group
	DeployDir   string `yaml:"deploy_dir"`   // Default: /opt/tikv
	DataDir     string `yaml:"data_dir"`     // Default: /data/tikv
	LogDir      string `yaml:"log_dir"`      // Default: /var/log/tikv
	
	// SSH configuration
	SSHPort         int    `yaml:"ssh_port"`
	SSHUser         string `yaml:"ssh_user"`
	SSHPrivateKey   string `yaml:"ssh_private_key"`
	SSHTimeout      string `yaml:"ssh_timeout"`
	
	// Security settings
	EnableTLS       bool   `yaml:"enable_tls"`
	CAPath          string `yaml:"ca_path,omitempty"`
	CertPath        string `yaml:"cert_path,omitempty"`
	KeyPath         string `yaml:"key_path,omitempty"`
	
	// Monitoring
	EnableMonitoring bool   `yaml:"enable_monitoring"`
	PrometheusPort   int    `yaml:"prometheus_port"`
	GrafanaPort      int    `yaml:"grafana_port"`
}

// Implement ComponentSpec interface for TiKV components
func (pd PDSpec) GetHost() string     { return pd.Host }
func (pd PDSpec) GetPort() int        { return pd.ClientPort }
func (pd PDSpec) GetDataDir() string  { return pd.DataDir }
func (pd PDSpec) GetUser() string     { return pd.User }
func (pd PDSpec) GetSSHPort() int     { return pd.SSHPort }

func (tikv TiKVSpec) GetHost() string     { return tikv.Host }
func (tikv TiKVSpec) GetPort() int        { return tikv.Port }
func (tikv TiKVSpec) GetDataDir() string  { return tikv.DataDir }
func (tikv TiKVSpec) GetUser() string     { return tikv.User }
func (tikv TiKVSpec) GetSSHPort() int     { return tikv.SSHPort }

func (tidb TiDBSpec) GetHost() string     { return tidb.Host }
func (tidb TiDBSpec) GetPort() int        { return tidb.Port }
func (tidb TiDBSpec) GetDataDir() string  { return "" } // TiDB is stateless
func (tidb TiDBSpec) GetUser() string     { return tidb.User }
func (tidb TiDBSpec) GetSSHPort() int     { return tidb.SSHPort }
