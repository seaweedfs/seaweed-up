package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/monitoring/alerting"
	"github.com/seaweedfs/seaweed-up/pkg/monitoring/metrics"
	"gopkg.in/yaml.v3"
)

// TemplateManager manages cluster configuration templates
type TemplateManager struct {
	templatesDir string
	templates    map[string]*ClusterTemplate
}

// ClusterTemplate represents a cluster configuration template
type ClusterTemplate struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Version     string                 `yaml:"version"`
	Author      string                 `yaml:"author"`
	Tags        []string               `yaml:"tags"`
	Parameters  []TemplateParameter    `yaml:"parameters"`
	Spec        spec.Specification     `yaml:"spec"`
	Monitoring  MonitoringConfig       `yaml:"monitoring,omitempty"`
	Variables   map[string]interface{} `yaml:"variables,omitempty"`
}

// TemplateParameter defines a configurable parameter
type TemplateParameter struct {
	Name         string      `yaml:"name"`
	Type         string      `yaml:"type"` // string, int, bool, array
	Description  string      `yaml:"description"`
	DefaultValue interface{} `yaml:"default"`
	Required     bool        `yaml:"required"`
	Options      []string    `yaml:"options,omitempty"`
	Validation   string      `yaml:"validation,omitempty"`
}

// MonitoringConfig defines monitoring configuration for templates
type MonitoringConfig struct {
	Enabled         bool                 `yaml:"enabled"`
	MetricsInterval string               `yaml:"metrics_interval"`
	AlertRules      []alerting.AlertRule `yaml:"alert_rules,omitempty"`
	Notifiers       []NotifierConfig     `yaml:"notifiers,omitempty"`
}

// NotifierConfig defines notification configuration
type NotifierConfig struct {
	Type   string                 `yaml:"type"` // console, email, webhook, slack
	Name   string                 `yaml:"name"`
	Config map[string]interface{} `yaml:"config"`
}

// NewTemplateManager creates a new template manager
func NewTemplateManager(templatesDir string) *TemplateManager {
	return &TemplateManager{
		templatesDir: templatesDir,
		templates:    make(map[string]*ClusterTemplate),
	}
}

// LoadTemplates loads all templates from the templates directory
func (tm *TemplateManager) LoadTemplates() error {
	if _, err := os.Stat(tm.templatesDir); os.IsNotExist(err) {
		// Create templates directory with built-in templates
		if err := os.MkdirAll(tm.templatesDir, 0755); err != nil {
			return fmt.Errorf("failed to create templates directory: %w", err)
		}

		// Create built-in templates
		if err := tm.createBuiltInTemplates(); err != nil {
			return fmt.Errorf("failed to create built-in templates: %w", err)
		}
	}

	// Load templates from directory
	return filepath.Walk(tm.templatesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(path, ".yaml") {
			template, err := tm.loadTemplate(path)
			if err != nil {
				fmt.Printf("Warning: failed to load template %s: %v\n", path, err)
				return nil // Continue loading other templates
			}

			tm.templates[template.Name] = template
		}

		return nil
	})
}

// loadTemplate loads a single template from a file
func (tm *TemplateManager) loadTemplate(filePath string) (*ClusterTemplate, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var template ClusterTemplate
	if err := yaml.Unmarshal(data, &template); err != nil {
		return nil, err
	}

	return &template, nil
}

// GetTemplate returns a template by name
func (tm *TemplateManager) GetTemplate(name string) (*ClusterTemplate, error) {
	template, exists := tm.templates[name]
	if !exists {
		return nil, fmt.Errorf("template '%s' not found", name)
	}

	return template, nil
}

// ListTemplates returns all available templates
func (tm *TemplateManager) ListTemplates() []*ClusterTemplate {
	templates := make([]*ClusterTemplate, 0, len(tm.templates))
	for _, template := range tm.templates {
		templates = append(templates, template)
	}
	return templates
}

// SaveTemplate saves a template to the templates directory
func (tm *TemplateManager) SaveTemplate(template *ClusterTemplate) error {
	fileName := fmt.Sprintf("%s.yaml", strings.ReplaceAll(template.Name, " ", "-"))
	filePath := filepath.Join(tm.templatesDir, fileName)

	data, err := yaml.Marshal(template)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return err
	}

	// Add to memory
	tm.templates[template.Name] = template

	return nil
}

// createBuiltInTemplates creates built-in templates
func (tm *TemplateManager) createBuiltInTemplates() error {
	templates := []*ClusterTemplate{
		tm.createSingleNodeTemplate(),
		tm.createDevelopmentTemplate(),
	}

	for _, template := range templates {
		if err := tm.SaveTemplate(template); err != nil {
			return err
		}
	}

	return nil
}

// createSingleNodeTemplate creates a single-node development template
func (tm *TemplateManager) createSingleNodeTemplate() *ClusterTemplate {
	return &ClusterTemplate{
		Name:        "single-node",
		Description: "Single node SeaweedFS deployment for development and testing",
		Version:     "1.0.0",
		Author:      "seaweed-up",
		Tags:        []string{"development", "testing", "single-node"},
		Parameters: []TemplateParameter{
			{
				Name:         "cluster_name",
				Type:         "string",
				Description:  "Name of the cluster",
				DefaultValue: "dev-cluster",
				Required:     true,
			},
			{
				Name:         "host",
				Type:         "string",
				Description:  "Host IP address",
				DefaultValue: "localhost",
				Required:     true,
			},
		},
		Spec: spec.Specification{
			Name: "{{.Parameters.cluster_name}}",
			MasterServers: []*spec.MasterServerSpec{
				{
					Host:    "{{.Parameters.host}}",
					Port:    9333,
					DataDir: "/opt/seaweedfs/master",
				},
			},
			VolumeServers: []*spec.VolumeServerSpec{
				{
					Host:    "{{.Parameters.host}}",
					Port:    8080,
					DataDir: "/opt/seaweedfs/volume",
				},
			},
			FilerServers: []*spec.FilerServerSpec{
				{
					Host:    "{{.Parameters.host}}",
					Port:    8888,
					DataDir: "/opt/seaweedfs/filer",
					S3:      true,
					S3Port:  8333,
				},
			},
		},
		Monitoring: MonitoringConfig{
			Enabled:         true,
			MetricsInterval: "30s",
		},
	}
}

// createDevelopmentTemplate creates a development cluster template
func (tm *TemplateManager) createDevelopmentTemplate() *ClusterTemplate {
	return &ClusterTemplate{
		Name:        "development",
		Description: "Multi-node development cluster with basic monitoring",
		Version:     "1.0.0",
		Author:      "seaweed-up",
		Tags:        []string{"development", "multi-node"},
		Parameters: []TemplateParameter{
			{
				Name:         "cluster_name",
				Type:         "string",
				Description:  "Name of the cluster",
				DefaultValue: "dev-cluster",
				Required:     true,
			},
		},
		Spec: spec.Specification{
			Name: "{{.Parameters.cluster_name}}",
			// Simplified spec for demo
		},
		Monitoring: MonitoringConfig{
			Enabled:         true,
			MetricsInterval: "15s",
			AlertRules: []alerting.AlertRule{
				{
					Name: "high-memory-usage",
					Query: metrics.MetricsQuery{
						MetricName: "memory_usage",
					},
					Condition:   alerting.ConditionGreaterThan,
					Threshold:   80.0,
					Severity:    alerting.SeverityWarning,
					Summary:     "High memory usage on {{.Host}}",
					Description: "Memory usage is above 80%",
					Enabled:     true,
				},
			},
			Notifiers: []NotifierConfig{
				{
					Type: "console",
					Name: "console-alerts",
				},
			},
		},
	}
}
