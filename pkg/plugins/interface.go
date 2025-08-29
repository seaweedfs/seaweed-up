package plugins

import (
	"context"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// Plugin represents a seaweed-up plugin with metadata and lifecycle hooks
type Plugin interface {
	// Metadata
	Name() string
	Version() string
	Description() string
	Author() string

	// Lifecycle hooks
	Initialize(ctx context.Context, config map[string]interface{}) error
	Validate(ctx context.Context) error
	Cleanup(ctx context.Context) error

	// Plugin capabilities
	SupportedOperations() []OperationType
	Execute(ctx context.Context, operation OperationType, params map[string]interface{}) (*OperationResult, error)
}

// OperationType represents the type of operation a plugin can handle
type OperationType string

const (
	OperationTypeDeploy    OperationType = "deploy"
	OperationTypeUpgrade   OperationType = "upgrade"
	OperationTypeScale     OperationType = "scale"
	OperationTypeMonitor   OperationType = "monitor"
	OperationTypeBackup    OperationType = "backup"
	OperationTypeRestore   OperationType = "restore"
	OperationTypeValidate  OperationType = "validate"
	OperationTypeExport    OperationType = "export"
	OperationTypeImport    OperationType = "import"
	OperationTypeCustom    OperationType = "custom"
)

// OperationResult contains the result of a plugin operation
type OperationResult struct {
	Success    bool                   `json:"success"`
	Message    string                 `json:"message"`
	Data       map[string]interface{} `json:"data,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Duration   time.Duration          `json:"duration"`
	Timestamp  time.Time              `json:"timestamp"`
}

// ClusterPlugin is a specialized plugin for cluster operations
type ClusterPlugin interface {
	Plugin

	// Cluster-specific operations
	ValidateCluster(ctx context.Context, cluster *spec.Specification) error
	PreDeploy(ctx context.Context, cluster *spec.Specification) error
	PostDeploy(ctx context.Context, cluster *spec.Specification) error
	PreUpgrade(ctx context.Context, cluster *spec.Specification, newVersion string) error
	PostUpgrade(ctx context.Context, cluster *spec.Specification, newVersion string) error
}

// MonitoringPlugin is a specialized plugin for monitoring operations
type MonitoringPlugin interface {
	Plugin

	// Monitoring-specific operations
	CollectMetrics(ctx context.Context, cluster *spec.Specification) (map[string]interface{}, error)
	CheckHealth(ctx context.Context, cluster *spec.Specification) (*HealthStatus, error)
	GenerateAlert(ctx context.Context, alert *AlertDefinition) error
}

// ExportPlugin is a specialized plugin for exporting cluster configurations
type ExportPlugin interface {
	Plugin

	// Export-specific operations
	SupportedFormats() []ExportFormat
	Export(ctx context.Context, cluster *spec.Specification, format ExportFormat) ([]byte, error)
	ValidateExport(ctx context.Context, data []byte, format ExportFormat) error
}

// ImportPlugin is a specialized plugin for importing cluster configurations
type ImportPlugin interface {
	Plugin

	// Import-specific operations
	SupportedFormats() []ImportFormat
	Import(ctx context.Context, data []byte, format ImportFormat) (*spec.Specification, error)
	ValidateImport(ctx context.Context, data []byte, format ImportFormat) error
}

// ExportFormat represents supported export formats
type ExportFormat string

const (
	ExportFormatKubernetes     ExportFormat = "kubernetes"
	ExportFormatDockerCompose  ExportFormat = "docker-compose"
	ExportFormatTerraform      ExportFormat = "terraform"
	ExportFormatAnsible        ExportFormat = "ansible"
	ExportFormatHelm           ExportFormat = "helm"
	ExportFormatNomad          ExportFormat = "nomad"
)

// ImportFormat represents supported import formats
type ImportFormat string

const (
	ImportFormatDockerCompose ImportFormat = "docker-compose"
	ImportFormatKubernetes    ImportFormat = "kubernetes"
	ImportFormatTerraform     ImportFormat = "terraform"
	ImportFormatJSON          ImportFormat = "json"
)

// HealthStatus represents the health status of cluster components
type HealthStatus struct {
	Overall    ComponentHealth            `json:"overall"`
	Components map[string]ComponentHealth `json:"components"`
	Message    string                     `json:"message"`
	Timestamp  time.Time                  `json:"timestamp"`
}

// ComponentHealth represents health status levels
type ComponentHealth string

const (
	HealthHealthy   ComponentHealth = "healthy"
	HealthWarning   ComponentHealth = "warning"
	HealthCritical  ComponentHealth = "critical"
	HealthUnknown   ComponentHealth = "unknown"
)

// AlertDefinition defines an alert to be generated
type AlertDefinition struct {
	Name        string                 `json:"name"`
	Severity    string                 `json:"severity"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Labels      map[string]string      `json:"labels"`
	Annotations map[string]string      `json:"annotations"`
	Data        map[string]interface{} `json:"data"`
}

// PluginManifest describes a plugin's metadata and requirements
type PluginManifest struct {
	Name         string            `yaml:"name"`
	Version      string            `yaml:"version"`
	Description  string            `yaml:"description"`
	Author       string            `yaml:"author"`
	Website      string            `yaml:"website,omitempty"`
	License      string            `yaml:"license,omitempty"`
	Binary       string            `yaml:"binary"`
	Checksum     string            `yaml:"checksum,omitempty"`
	Dependencies []string          `yaml:"dependencies,omitempty"`
	Platforms    []PlatformSupport `yaml:"platforms"`
	Config       PluginConfig      `yaml:"config,omitempty"`
}

// PlatformSupport describes supported platforms
type PlatformSupport struct {
	OS   string `yaml:"os"`
	Arch string `yaml:"arch"`
}

// PluginConfig describes plugin configuration options
type PluginConfig struct {
	Required []ConfigOption `yaml:"required,omitempty"`
	Optional []ConfigOption `yaml:"optional,omitempty"`
}

// ConfigOption describes a configuration option
type ConfigOption struct {
	Name        string      `yaml:"name"`
	Type        string      `yaml:"type"`
	Description string      `yaml:"description"`
	Default     interface{} `yaml:"default,omitempty"`
	Required    bool        `yaml:"required"`
	Options     []string    `yaml:"options,omitempty"`
}

// PluginRegistry interface for plugin discovery and management
type PluginRegistry interface {
	// Plugin discovery
	ListPlugins() ([]*PluginManifest, error)
	GetPlugin(name string) (*PluginManifest, error)
	SearchPlugins(query string) ([]*PluginManifest, error)

	// Plugin lifecycle
	InstallPlugin(name, version string) error
	UpdatePlugin(name string) error
	UninstallPlugin(name string) error
	EnablePlugin(name string) error
	DisablePlugin(name string) error

	// Plugin information
	GetInstalledPlugins() ([]*PluginManifest, error)
	GetPluginStatus(name string) (PluginStatus, error)
}

// PluginStatus represents the status of an installed plugin
type PluginStatus struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Enabled   bool      `json:"enabled"`
	Loaded    bool      `json:"loaded"`
	Error     string    `json:"error,omitempty"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

// PluginManager handles plugin lifecycle and execution
type PluginManager interface {
	// Plugin management
	LoadPlugin(manifest *PluginManifest) error
	UnloadPlugin(name string) error
	ReloadPlugin(name string) error

	// Plugin execution
	ExecutePlugin(name string, operation OperationType, params map[string]interface{}) (*OperationResult, error)
	GetLoadedPlugin(name string) (Plugin, error)
	ListLoadedPlugins() []Plugin

	// Plugin hooks
	RegisterHook(operation OperationType, plugin string) error
	UnregisterHook(operation OperationType, plugin string) error
	ExecuteHooks(ctx context.Context, operation OperationType, params map[string]interface{}) ([]*OperationResult, error)

	// Plugin validation
	ValidatePlugin(manifest *PluginManifest) error
	TestPlugin(name string) error
}

// HookExecutor executes plugin hooks at specific lifecycle points
type HookExecutor interface {
	ExecutePreHooks(ctx context.Context, operation OperationType, params map[string]interface{}) error
	ExecutePostHooks(ctx context.Context, operation OperationType, params map[string]interface{}) error
	ExecuteValidationHooks(ctx context.Context, cluster *spec.Specification) error
}
