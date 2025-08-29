package plugins

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPluginManager implements PluginManager interface
type DefaultPluginManager struct {
	pluginsDir    string
	loadedPlugins map[string]Plugin
	manifests     map[string]*PluginManifest
	hooks         map[OperationType][]string
	registry      PluginRegistry
	mu            sync.RWMutex
}

// NewPluginManager creates a new plugin manager
func NewPluginManager(pluginsDir string, registry PluginRegistry) *DefaultPluginManager {
	return &DefaultPluginManager{
		pluginsDir:    pluginsDir,
		loadedPlugins: make(map[string]Plugin),
		manifests:     make(map[string]*PluginManifest),
		hooks:         make(map[OperationType][]string),
		registry:      registry,
	}
}

// Initialize initializes the plugin manager and discovers available plugins
func (pm *DefaultPluginManager) Initialize() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Create plugins directory if it doesn't exist
	if err := os.MkdirAll(pm.pluginsDir, 0755); err != nil {
		return fmt.Errorf("failed to create plugins directory: %w", err)
	}

	// Discover and load plugins
	return pm.discoverPlugins()
}

// discoverPlugins scans the plugins directory for available plugins
func (pm *DefaultPluginManager) discoverPlugins() error {
	entries, err := os.ReadDir(pm.pluginsDir)
	if err != nil {
		return fmt.Errorf("failed to read plugins directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginDir := filepath.Join(pm.pluginsDir, entry.Name())
		manifestPath := filepath.Join(pluginDir, "plugin.yaml")

		// Check if plugin manifest exists
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		}

		// Load plugin manifest
		manifest, err := pm.loadManifest(manifestPath)
		if err != nil {
			fmt.Printf("Warning: failed to load plugin manifest %s: %v\n", manifestPath, err)
			continue
		}

		pm.manifests[manifest.Name] = manifest

		// Auto-load enabled plugins
		if pm.isPluginEnabled(manifest.Name) {
			if err := pm.loadPluginFromManifest(manifest); err != nil {
				fmt.Printf("Warning: failed to load plugin %s: %v\n", manifest.Name, err)
			}
		}
	}

	return nil
}

// loadManifest loads a plugin manifest from file
func (pm *DefaultPluginManager) loadManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file: %w", err)
	}

	var manifest PluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

// LoadPlugin loads a plugin from its manifest
func (pm *DefaultPluginManager) LoadPlugin(manifest *PluginManifest) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	return pm.loadPluginFromManifest(manifest)
}

// loadPluginFromManifest loads a plugin from its manifest (internal, assumes lock held)
func (pm *DefaultPluginManager) loadPluginFromManifest(manifest *PluginManifest) error {
	// Check if plugin is already loaded
	if _, exists := pm.loadedPlugins[manifest.Name]; exists {
		return fmt.Errorf("plugin %s is already loaded", manifest.Name)
	}

	// Validate plugin manifest
	if err := pm.ValidatePlugin(manifest); err != nil {
		return fmt.Errorf("plugin validation failed: %w", err)
	}

	// Create external plugin instance
	plugin := &ExternalPlugin{
		manifest:   manifest,
		pluginsDir: pm.pluginsDir,
	}

	// Initialize plugin
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := plugin.Initialize(ctx, make(map[string]interface{})); err != nil {
		return fmt.Errorf("plugin initialization failed: %w", err)
	}

	pm.loadedPlugins[manifest.Name] = plugin
	return nil
}

// UnloadPlugin unloads a plugin
func (pm *DefaultPluginManager) UnloadPlugin(name string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	plugin, exists := pm.loadedPlugins[name]
	if !exists {
		return fmt.Errorf("plugin %s is not loaded", name)
	}

	// Cleanup plugin
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := plugin.Cleanup(ctx); err != nil {
		fmt.Printf("Warning: plugin cleanup failed: %v\n", err)
	}

	delete(pm.loadedPlugins, name)
	return nil
}

// ReloadPlugin reloads a plugin
func (pm *DefaultPluginManager) ReloadPlugin(name string) error {
	if err := pm.UnloadPlugin(name); err != nil {
		// If plugin wasn't loaded, that's okay
	}

	manifest, exists := pm.manifests[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	return pm.LoadPlugin(manifest)
}

// ExecutePlugin executes a specific plugin operation
func (pm *DefaultPluginManager) ExecutePlugin(name string, operation OperationType, params map[string]interface{}) (*OperationResult, error) {
	pm.mu.RLock()
	plugin, exists := pm.loadedPlugins[name]
	pm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("plugin %s is not loaded", name)
	}

	// Check if plugin supports this operation
	supported := false
	for _, op := range plugin.SupportedOperations() {
		if op == operation {
			supported = true
			break
		}
	}

	if !supported {
		return nil, fmt.Errorf("plugin %s does not support operation %s", name, operation)
	}

	// Execute plugin operation
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	start := time.Now()
	result, err := plugin.Execute(ctx, operation, params)
	
	if result != nil {
		result.Duration = time.Since(start)
		result.Timestamp = time.Now()
	}

	return result, err
}

// GetLoadedPlugin returns a loaded plugin by name
func (pm *DefaultPluginManager) GetLoadedPlugin(name string) (Plugin, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	plugin, exists := pm.loadedPlugins[name]
	if !exists {
		return nil, fmt.Errorf("plugin %s is not loaded", name)
	}

	return plugin, nil
}

// ListLoadedPlugins returns all loaded plugins
func (pm *DefaultPluginManager) ListLoadedPlugins() []Plugin {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	plugins := make([]Plugin, 0, len(pm.loadedPlugins))
	for _, plugin := range pm.loadedPlugins {
		plugins = append(plugins, plugin)
	}

	return plugins
}

// RegisterHook registers a plugin for a specific operation hook
func (pm *DefaultPluginManager) RegisterHook(operation OperationType, plugin string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if plugin is loaded
	if _, exists := pm.loadedPlugins[plugin]; !exists {
		return fmt.Errorf("plugin %s is not loaded", plugin)
	}

	// Add plugin to hooks for this operation
	pm.hooks[operation] = append(pm.hooks[operation], plugin)
	return nil
}

// UnregisterHook unregisters a plugin from an operation hook
func (pm *DefaultPluginManager) UnregisterHook(operation OperationType, plugin string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	hooks := pm.hooks[operation]
	for i, hookPlugin := range hooks {
		if hookPlugin == plugin {
			// Remove plugin from hooks
			pm.hooks[operation] = append(hooks[:i], hooks[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("plugin %s is not registered for operation %s", plugin, operation)
}

// ExecuteHooks executes all registered hooks for an operation
func (pm *DefaultPluginManager) ExecuteHooks(ctx context.Context, operation OperationType, params map[string]interface{}) ([]*OperationResult, error) {
	pm.mu.RLock()
	hookPlugins := make([]string, len(pm.hooks[operation]))
	copy(hookPlugins, pm.hooks[operation])
	pm.mu.RUnlock()

	var results []*OperationResult
	var errors []error

	for _, pluginName := range hookPlugins {
		result, err := pm.ExecutePlugin(pluginName, operation, params)
		if err != nil {
			errors = append(errors, fmt.Errorf("hook %s failed: %w", pluginName, err))
			continue
		}

		results = append(results, result)
	}

	// Return first error if any hooks failed
	if len(errors) > 0 {
		return results, errors[0]
	}

	return results, nil
}

// ValidatePlugin validates a plugin manifest
func (pm *DefaultPluginManager) ValidatePlugin(manifest *PluginManifest) error {
	if manifest.Name == "" {
		return fmt.Errorf("plugin name is required")
	}

	if manifest.Version == "" {
		return fmt.Errorf("plugin version is required")
	}

	if manifest.Binary == "" {
		return fmt.Errorf("plugin binary is required")
	}

	// Check if binary exists
	pluginDir := filepath.Join(pm.pluginsDir, manifest.Name)
	binaryPath := filepath.Join(pluginDir, manifest.Binary)
	
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("plugin binary not found: %s", binaryPath)
	}

	// Check if binary is executable
	if info, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("cannot stat plugin binary: %w", err)
	} else if info.Mode()&0111 == 0 {
		return fmt.Errorf("plugin binary is not executable: %s", binaryPath)
	}

	return nil
}

// TestPlugin tests a plugin by executing a simple validation
func (pm *DefaultPluginManager) TestPlugin(name string) error {
	plugin, err := pm.GetLoadedPlugin(name)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return plugin.Validate(ctx)
}

// isPluginEnabled checks if a plugin is enabled
func (pm *DefaultPluginManager) isPluginEnabled(name string) bool {
	// For now, assume all discovered plugins are enabled
	// This could be enhanced with a configuration file
	return true
}

// ExternalPlugin represents a plugin that runs as an external process
type ExternalPlugin struct {
	manifest   *PluginManifest
	pluginsDir string
}

// Name returns the plugin name
func (ep *ExternalPlugin) Name() string {
	return ep.manifest.Name
}

// Version returns the plugin version
func (ep *ExternalPlugin) Version() string {
	return ep.manifest.Version
}

// Description returns the plugin description
func (ep *ExternalPlugin) Description() string {
	return ep.manifest.Description
}

// Author returns the plugin author
func (ep *ExternalPlugin) Author() string {
	return ep.manifest.Author
}

// Initialize initializes the external plugin
func (ep *ExternalPlugin) Initialize(ctx context.Context, config map[string]interface{}) error {
	return ep.executeCommand(ctx, "init", config)
}

// Validate validates the external plugin
func (ep *ExternalPlugin) Validate(ctx context.Context) error {
	return ep.executeCommand(ctx, "validate", nil)
}

// Cleanup cleans up the external plugin
func (ep *ExternalPlugin) Cleanup(ctx context.Context) error {
	return ep.executeCommand(ctx, "cleanup", nil)
}

// SupportedOperations returns the operations this plugin supports
func (ep *ExternalPlugin) SupportedOperations() []OperationType {
	// This could be enhanced to query the plugin for supported operations
	// For now, return common operations
	return []OperationType{
		OperationTypeDeploy,
		OperationTypeUpgrade,
		OperationTypeValidate,
		OperationTypeCustom,
	}
}

// Execute executes a plugin operation
func (ep *ExternalPlugin) Execute(ctx context.Context, operation OperationType, params map[string]interface{}) (*OperationResult, error) {
	// Combine operation and parameters
	operationParams := map[string]interface{}{
		"operation": string(operation),
		"params":    params,
	}

	err := ep.executeCommand(ctx, "execute", operationParams)
	
	result := &OperationResult{
		Success:   err == nil,
		Timestamp: time.Now(),
	}

	if err != nil {
		result.Error = err.Error()
		result.Message = fmt.Sprintf("Plugin %s operation %s failed", ep.Name(), operation)
	} else {
		result.Message = fmt.Sprintf("Plugin %s operation %s completed successfully", ep.Name(), operation)
	}

	return result, err
}

// executeCommand executes a plugin command
func (ep *ExternalPlugin) executeCommand(ctx context.Context, command string, params map[string]interface{}) error {
	pluginDir := filepath.Join(ep.pluginsDir, ep.manifest.Name)
	binaryPath := filepath.Join(pluginDir, ep.manifest.Binary)

	cmd := exec.CommandContext(ctx, binaryPath, command)
	cmd.Dir = pluginDir

	// Set environment variables if needed
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("SEAWEED_UP_PLUGIN=%s", ep.manifest.Name),
		fmt.Sprintf("SEAWEED_UP_VERSION=%s", ep.manifest.Version),
	)

	// Execute command
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("plugin command failed: %w, output: %s", err, string(output))
	}

	return nil
}
