package errors

import "fmt"

// Base error types following TiUP's structured error pattern

// SeaweedUpError is the base error type for all seaweed-up errors
type SeaweedUpError struct {
	Code    string
	Message string
	Cause   error
	Context map[string]interface{}
}

func (e *SeaweedUpError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

// Unwrap returns the underlying cause error for error wrapping
func (e *SeaweedUpError) Unwrap() error {
	return e.Cause
}

// ClusterOperationError represents errors during cluster operations
type ClusterOperationError struct {
	*SeaweedUpError
	Operation   string // deploy, upgrade, scale, etc.
	ClusterName string
	Node        string // optional, specific node where error occurred
}

func NewClusterOperationError(operation, clusterName, node string, cause error) *ClusterOperationError {
	return &ClusterOperationError{
		SeaweedUpError: &SeaweedUpError{
			Code:    "CLUSTER_OPERATION_ERROR",
			Message: fmt.Sprintf("cluster operation '%s' failed", operation),
			Cause:   cause,
			Context: map[string]interface{}{
				"operation":    operation,
				"cluster_name": clusterName,
				"node":         node,
			},
		},
		Operation:   operation,
		ClusterName: clusterName,
		Node:        node,
	}
}

// SSHConnectionError represents SSH connection failures
type SSHConnectionError struct {
	*SeaweedUpError
	Host string
	Port int
	User string
}

func NewSSHConnectionError(host string, port int, user string, cause error) *SSHConnectionError {
	return &SSHConnectionError{
		SeaweedUpError: &SeaweedUpError{
			Code:    "SSH_CONNECTION_ERROR",
			Message: fmt.Sprintf("failed to connect to %s:%d as user %s", host, port, user),
			Cause:   cause,
			Context: map[string]interface{}{
				"host": host,
				"port": port,
				"user": user,
			},
		},
		Host: host,
		Port: port,
		User: user,
	}
}

// ComponentError represents component management errors
type ComponentError struct {
	*SeaweedUpError
	Component string // master, volume, filer, etc.
	Version   string
	Action    string // install, uninstall, update, etc.
}

func NewComponentError(component, version, action string, cause error) *ComponentError {
	return &ComponentError{
		SeaweedUpError: &SeaweedUpError{
			Code:    "COMPONENT_ERROR",
			Message: fmt.Sprintf("component '%s' %s failed", component, action),
			Cause:   cause,
			Context: map[string]interface{}{
				"component": component,
				"version":   version,
				"action":    action,
			},
		},
		Component: component,
		Version:   version,
		Action:    action,
	}
}

// ConfigurationError represents configuration validation errors
type ConfigurationError struct {
	*SeaweedUpError
	Path        string // YAML path where error occurred
	Suggestions []string
}

func NewConfigurationError(path, message string, suggestions []string, cause error) *ConfigurationError {
	return &ConfigurationError{
		SeaweedUpError: &SeaweedUpError{
			Code:    "CONFIGURATION_ERROR",
			Message: message,
			Cause:   cause,
			Context: map[string]interface{}{
				"path":        path,
				"suggestions": suggestions,
			},
		},
		Path:        path,
		Suggestions: suggestions,
	}
}

// EnvironmentError represents environment management errors
type EnvironmentError struct {
	*SeaweedUpError
	Environment string
	Action      string
}

func NewEnvironmentError(environment, action string, cause error) *EnvironmentError {
	return &EnvironmentError{
		SeaweedUpError: &SeaweedUpError{
			Code:    "ENVIRONMENT_ERROR",
			Message: fmt.Sprintf("environment '%s' %s failed", environment, action),
			Cause:   cause,
			Context: map[string]interface{}{
				"environment": environment,
				"action":      action,
			},
		},
		Environment: environment,
		Action:      action,
	}
}
