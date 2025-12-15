package environment

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/mitchellh/go-homedir"
	"github.com/seaweedfs/seaweed-up/pkg/errors"
)

// Environment represents the global environment for seaweed-up
type Environment struct {
	HomeDir     string
	ConfigDir   string
	DataDir     string
	CacheDir    string
	ProfileName string
}

// globalEnv holds the singleton environment instance.
// Note: Using a global variable simplifies CLI command access but introduces
// global state. For a CLI tool this is acceptable, but for better testability
// consider passing Environment explicitly via dependency injection in the future.
var (
	globalEnv     *Environment
	globalEnvOnce sync.Once
	globalEnvErr  error
)

// InitGlobalEnv initializes the global environment in a thread-safe manner.
// It uses sync.Once to ensure initialization happens exactly once,
// even if called concurrently from multiple goroutines.
func InitGlobalEnv() error {
	globalEnvOnce.Do(func() {
		homeDir, err := homedir.Dir()
		if err != nil {
			globalEnvErr = errors.NewEnvironmentError("default", "init", err)
			return
		}

		seaweedUpHome := filepath.Join(homeDir, ".seaweed-up")

		env := &Environment{
			HomeDir:     homeDir,
			ConfigDir:   filepath.Join(seaweedUpHome, "config"),
			DataDir:     filepath.Join(seaweedUpHome, "data"),
			CacheDir:    filepath.Join(seaweedUpHome, "cache"),
			ProfileName: "default",
		}

		// Create directories if they don't exist
		dirs := []string{env.ConfigDir, env.DataDir, env.CacheDir}
		for _, dir := range dirs {
			if err := os.MkdirAll(dir, 0755); err != nil {
				globalEnvErr = errors.NewEnvironmentError("default", "create_directories", err)
				return
			}
		}

		globalEnv = env
	})
	return globalEnvErr
}

// GlobalEnv returns the global environment instance
func GlobalEnv() *Environment {
	return globalEnv
}

// GetClusterRegistry returns the path to cluster registry
func (e *Environment) GetClusterRegistry() string {
	return filepath.Join(e.DataDir, "clusters.json")
}

// GetComponentCache returns the path to component cache directory
func (e *Environment) GetComponentCache() string {
	return filepath.Join(e.CacheDir, "components")
}

// GetEnvironmentDir returns the environment-specific directory
func (e *Environment) GetEnvironmentDir(envName string) string {
	return filepath.Join(e.DataDir, "environments", envName)
}

// GetConfigFile returns the path to the main config file
func (e *Environment) GetConfigFile() string {
	return filepath.Join(e.ConfigDir, "config.yaml")
}
