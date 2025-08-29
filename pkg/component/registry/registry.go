package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/environment"
	"github.com/seaweedfs/seaweed-up/pkg/errors"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
)

// InstalledComponent represents a locally installed component
type InstalledComponent struct {
	Name      string            `json:"name"`
	Version   string            `json:"version"`
	Path      string            `json:"path"`
	Metadata  map[string]string `json:"metadata"`
	InstallAt time.Time         `json:"install_at"`
	Size      int64             `json:"size"`
	Checksum  string            `json:"checksum,omitempty"`
}

// ComponentRegistry manages locally installed components
type ComponentRegistry struct {
	registryFile string
	cacheDir     string
	components   map[string][]*InstalledComponent // name -> versions
}

// NewComponentRegistry creates a new component registry
func NewComponentRegistry() (*ComponentRegistry, error) {
	env := environment.GlobalEnv()
	if env == nil {
		return nil, fmt.Errorf("global environment not initialized")
	}
	
	registryFile := filepath.Join(env.DataDir, "components.json")
	cacheDir := env.GetComponentCache()
	
	if err := utils.EnsureDir(cacheDir); err != nil {
		return nil, errors.NewComponentError("", "", "init_cache", err)
	}
	
	registry := &ComponentRegistry{
		registryFile: registryFile,
		cacheDir:     cacheDir,
		components:   make(map[string][]*InstalledComponent),
	}
	
	if err := registry.load(); err != nil {
		return nil, err
	}
	
	return registry, nil
}

// Install adds a component to the registry
func (r *ComponentRegistry) Install(component *InstalledComponent) error {
	// Check if already installed
	if r.IsInstalled(component.Name, component.Version) {
		return fmt.Errorf("component %s:%s is already installed", component.Name, component.Version)
	}
	
	// Add to registry
	r.components[component.Name] = append(r.components[component.Name], component)
	
	// Save registry
	return r.save()
}

// Uninstall removes a component from the registry and filesystem
func (r *ComponentRegistry) Uninstall(name, version string) error {
	components := r.components[name]
	if components == nil {
		return errors.NewComponentError(name, version, "uninstall", fmt.Errorf("component not found"))
	}
	
	// Find and remove the component
	for i, comp := range components {
		if comp.Version == version {
			// Remove binary file
			if utils.IsExist(comp.Path) {
				if err := os.Remove(comp.Path); err != nil {
					return errors.NewComponentError(name, version, "uninstall", err)
				}
			}
			
			// Remove from slice
			r.components[name] = append(components[:i], components[i+1:]...)
			
			// If no more versions, remove the key
			if len(r.components[name]) == 0 {
				delete(r.components, name)
			}
			
			return r.save()
		}
	}
	
	return errors.NewComponentError(name, version, "uninstall", fmt.Errorf("version not found"))
}

// IsInstalled checks if a component version is installed
func (r *ComponentRegistry) IsInstalled(name, version string) bool {
	components := r.components[name]
	if components == nil {
		return false
	}
	
	for _, comp := range components {
		if comp.Version == version {
			return true
		}
	}
	return false
}

// GetInstalled returns an installed component
func (r *ComponentRegistry) GetInstalled(name, version string) (*InstalledComponent, error) {
	components := r.components[name]
	if components == nil {
		return nil, errors.NewComponentError(name, version, "get", fmt.Errorf("component not found"))
	}
	
	for _, comp := range components {
		if comp.Version == version {
			return comp, nil
		}
	}
	
	return nil, errors.NewComponentError(name, version, "get", fmt.Errorf("version not found"))
}

// ListInstalled returns all installed components
func (r *ComponentRegistry) ListInstalled() map[string][]*InstalledComponent {
	// Return a copy to prevent modification
	result := make(map[string][]*InstalledComponent)
	for name, components := range r.components {
		result[name] = make([]*InstalledComponent, len(components))
		copy(result[name], components)
	}
	return result
}

// ListVersions returns all installed versions of a component
func (r *ComponentRegistry) ListVersions(name string) []string {
	components := r.components[name]
	if components == nil {
		return nil
	}
	
	versions := make([]string, len(components))
	for i, comp := range components {
		versions[i] = comp.Version
	}
	return versions
}

// GetLatestInstalled returns the latest installed version of a component
func (r *ComponentRegistry) GetLatestInstalled(name string) (*InstalledComponent, error) {
	components := r.components[name]
	if components == nil || len(components) == 0 {
		return nil, errors.NewComponentError(name, "", "get_latest", fmt.Errorf("no versions installed"))
	}
	
	// Simple approach: return the most recently installed
	// TODO: Implement proper semantic version comparison
	var latest *InstalledComponent
	for _, comp := range components {
		if latest == nil || comp.InstallAt.After(latest.InstallAt) {
			latest = comp
		}
	}
	
	return latest, nil
}

// GetBinaryPath returns the path to a component binary
func (r *ComponentRegistry) GetBinaryPath(name, version string) (string, error) {
	component, err := r.GetInstalled(name, version)
	if err != nil {
		return "", err
	}
	
	if !utils.IsExist(component.Path) {
		return "", errors.NewComponentError(name, version, "get_binary", fmt.Errorf("binary file not found at %s", component.Path))
	}
	
	return component.Path, nil
}

// GetComponentDir returns the component cache directory
func (r *ComponentRegistry) GetComponentDir(name, version string) string {
	return filepath.Join(r.cacheDir, name, version)
}

// GetBinaryFilename returns the expected binary filename
func (r *ComponentRegistry) GetBinaryFilename() string {
	return "weed" // SeaweedFS uses a single binary
}

// load reads the registry from disk
func (r *ComponentRegistry) load() error {
	if !utils.IsExist(r.registryFile) {
		// Create empty registry
		return r.save()
	}
	
	data, err := os.ReadFile(r.registryFile)
	if err != nil {
		return errors.NewComponentError("", "", "load_registry", err)
	}
	
	if err := json.Unmarshal(data, &r.components); err != nil {
		return errors.NewComponentError("", "", "load_registry", err)
	}
	
	return nil
}

// save writes the registry to disk
func (r *ComponentRegistry) save() error {
	data, err := json.MarshalIndent(r.components, "", "  ")
	if err != nil {
		return errors.NewComponentError("", "", "save_registry", err)
	}
	
	if err := utils.EnsureDir(filepath.Dir(r.registryFile)); err != nil {
		return errors.NewComponentError("", "", "save_registry", err)
	}
	
	if err := os.WriteFile(r.registryFile, data, 0644); err != nil {
		return errors.NewComponentError("", "", "save_registry", err)
	}
	
	return nil
}

// Stats returns registry statistics
func (r *ComponentRegistry) Stats() map[string]interface{} {
	totalComponents := len(r.components)
	totalVersions := 0
	totalSize := int64(0)
	
	for _, versions := range r.components {
		totalVersions += len(versions)
		for _, comp := range versions {
			totalSize += comp.Size
		}
	}
	
	return map[string]interface{}{
		"total_components": totalComponents,
		"total_versions":   totalVersions,
		"total_size":       utils.FormatBytes(totalSize),
		"cache_dir":        r.cacheDir,
	}
}
