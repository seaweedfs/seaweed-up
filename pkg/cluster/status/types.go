package status

import (
	"time"
)

// ClusterState represents the overall state of a cluster
type ClusterState string

const (
	StateRunning  ClusterState = "Running"
	StateStopped  ClusterState = "Stopped"
	StateDegraded ClusterState = "Degraded"
	StateError    ClusterState = "Error"
	StateUnknown  ClusterState = "Unknown"
)

// ComponentType represents different SeaweedFS components
type ComponentType string

const (
	ComponentMaster ComponentType = "master"
	ComponentVolume ComponentType = "volume"
	ComponentFiler  ComponentType = "filer"
	ComponentS3     ComponentType = "s3"
	ComponentWebDAV ComponentType = "webdav"
	ComponentMount  ComponentType = "mount"
)

// ClusterStatus represents the overall status of a SeaweedFS cluster
type ClusterStatus struct {
	Name       string            `json:"name"`
	State      ClusterState      `json:"state"`
	Components []ComponentStatus `json:"components"`
	CreatedAt  time.Time         `json:"created_at,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Version    string            `json:"version,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ComponentStatus represents the status of a single component
type ComponentStatus struct {
	Name        string        `json:"name"`
	Type        ComponentType `json:"type"`
	Host        string        `json:"host"`
	Port        int           `json:"port"`
	PID         int           `json:"pid"`
	Status      string        `json:"status"`
	Version     string        `json:"version,omitempty"`
	StartTime   time.Time     `json:"start_time,omitempty"`
	Uptime      time.Duration `json:"uptime"`
	MemoryUsage int64         `json:"memory_usage"`   // in bytes
	CPUUsage    float64       `json:"cpu_usage"`      // percentage
	DiskUsage   int64         `json:"disk_usage"`     // in bytes
	NetworkIO   NetworkStats  `json:"network_io"`
	HealthCheck HealthStatus  `json:"health_check"`
	LastSeen    time.Time     `json:"last_seen"`
}

// NetworkStats represents network I/O statistics
type NetworkStats struct {
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
	PacketsIn  int64 `json:"packets_in"`
	PacketsOut int64 `json:"packets_out"`
}

// HealthStatus represents component health check results
type HealthStatus struct {
	Status     string        `json:"status"`      // "healthy", "unhealthy", "warning"
	Latency    time.Duration `json:"latency"`     // response time
	Error      string        `json:"error,omitempty"`
	LastCheck  time.Time     `json:"last_check"`
	CheckCount int           `json:"check_count"`
	Metadata   interface{}   `json:"metadata,omitempty"`
}

// VolumeInfo represents volume server specific information
type VolumeInfo struct {
	VolumeCount     int           `json:"volume_count"`
	UsedSpace       int64         `json:"used_space"`
	AvailableSpace  int64         `json:"available_space"`
	TotalSpace      int64         `json:"total_space"`
	DataDirectories []string      `json:"data_directories"`
	Topology        TopologyInfo  `json:"topology,omitempty"`
}

// MasterInfo represents master server specific information
type MasterInfo struct {
	Leader         bool         `json:"leader"`
	PeerCount      int          `json:"peer_count"`
	VolumeServers  []string     `json:"volume_servers"`
	Topology       TopologyInfo `json:"topology"`
	ReplicationMap map[string]int `json:"replication_map"`
}

// FilerInfo represents filer server specific information
type FilerInfo struct {
	Store      string `json:"store"`      // backend store type
	MasterHost string `json:"master_host"`
	S3Enabled  bool   `json:"s3_enabled"`
	WebDAVEnabled bool `json:"webdav_enabled"`
}

// TopologyInfo represents cluster topology information
type TopologyInfo struct {
	DataCenters map[string]DataCenterInfo `json:"data_centers"`
	Replication string                    `json:"replication"`
	Version     string                    `json:"version"`
}

// DataCenterInfo represents data center information
type DataCenterInfo struct {
	Racks map[string]RackInfo `json:"racks"`
}

// RackInfo represents rack information
type RackInfo struct {
	Nodes map[string]NodeInfo `json:"nodes"`
}

// NodeInfo represents node information
type NodeInfo struct {
	VolumeCount   int    `json:"volume_count"`
	ActiveVolumes int    `json:"active_volumes"`
	MaxVolumes    int    `json:"max_volumes"`
	FreeSpace     int64  `json:"free_space"`
	Status        string `json:"status"`
}

// StatusCollectionOptions represents options for status collection
type StatusCollectionOptions struct {
	Timeout        time.Duration `json:"timeout"`
	IncludeMetrics bool          `json:"include_metrics"`
	Verbose        bool          `json:"verbose"`
	HealthCheck    bool          `json:"health_check"`
}

// StatusSummary represents a summary of cluster status
type StatusSummary struct {
	TotalComponents      int                    `json:"total_components"`
	RunningComponents    int                    `json:"running_components"`
	HealthyComponents    int                    `json:"healthy_components"`
	ComponentsByType     map[ComponentType]int  `json:"components_by_type"`
	ComponentsByStatus   map[string]int         `json:"components_by_status"`
	TotalMemoryUsage     int64                  `json:"total_memory_usage"`
	TotalCPUUsage        float64                `json:"total_cpu_usage"`
	TotalDiskUsage       int64                  `json:"total_disk_usage"`
	AverageResponseTime  time.Duration          `json:"average_response_time"`
	ClusterVersion       string                 `json:"cluster_version"`
	LastUpdated          time.Time              `json:"last_updated"`
}
