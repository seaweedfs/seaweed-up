package task

import (
	"context"
	"fmt"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/executor"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/status"
	"github.com/seaweedfs/seaweed-up/pkg/component/registry"
)

// DeployComponentTask deploys a SeaweedFS component
type DeployComponentTask struct {
	BaseTask
	Component spec.ComponentSpec
	Version   string
	ConfigDir string
	DataDir   string
	Executor  executor.Executor
	Registry  *registry.ComponentRegistry
}

// Execute deploys the component
func (t *DeployComponentTask) Execute(ctx context.Context) error {
	// Get binary path from registry
	binaryPath, err := t.Registry.GetBinaryPath("seaweedfs", t.Version)
	if err != nil {
		return fmt.Errorf("binary not found for version %s: %w", t.Version, err)
	}

	// Create directories
	if err := t.createDirectories(ctx); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Copy binary to target host
	if err := t.deployBinary(ctx, binaryPath); err != nil {
		return fmt.Errorf("failed to deploy binary: %w", err)
	}

	// Generate configuration
	if err := t.generateConfig(ctx); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	// Create systemd service
	if err := t.createSystemdService(ctx); err != nil {
		return fmt.Errorf("failed to create systemd service: %w", err)
	}

	// Start service
	if err := t.startService(ctx); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	// Wait for service to be ready
	return t.waitForHealthy(ctx)
}

// Rollback removes the deployed component
func (t *DeployComponentTask) Rollback(ctx context.Context) error {
	// Stop service
	t.stopService(ctx) // Don't fail rollback if stop fails

	// Remove systemd service
	t.removeSystemdService(ctx)

	// Remove binary and config (optional, could be kept for debugging)
	return nil
}

// createDirectories creates necessary directories on the target host
func (t *DeployComponentTask) createDirectories(ctx context.Context) error {
	dirs := []string{t.ConfigDir, t.Component.GetDataDir(), "/var/log/seaweedfs"}

	for _, dir := range dirs {
		cmd := fmt.Sprintf("sudo mkdir -p %s && sudo chown $(whoami):$(whoami) %s", dir, dir)
		_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
		if err != nil {
			return err
		}
	}

	return nil
}

// deployBinary copies the binary to the target host
func (t *DeployComponentTask) deployBinary(ctx context.Context, binaryPath string) error {
	// For now, this is a placeholder
	// In a real implementation, you would use SCP or similar to copy the binary
	targetPath := fmt.Sprintf("%s/weed", t.ConfigDir)
	cmd := fmt.Sprintf("echo 'Binary deployment placeholder for %s'", targetPath)

	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

// generateConfig generates component configuration
func (t *DeployComponentTask) generateConfig(ctx context.Context) error {
	configContent := t.generateConfigContent()
	configPath := fmt.Sprintf("%s/%s.toml", t.ConfigDir, t.Component.GetType())

	// Write config file
	cmd := fmt.Sprintf("cat > %s << 'EOF'\n%s\nEOF", configPath, configContent)
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

// generateConfigContent creates the configuration file content
func (t *DeployComponentTask) generateConfigContent() string {
	// This would generate appropriate configuration based on component type
	// For now, return a placeholder
	return fmt.Sprintf("# SeaweedFS %s configuration\n", t.Component.GetType())
}

// createSystemdService creates a systemd service file
func (t *DeployComponentTask) createSystemdService(ctx context.Context) error {
	serviceName := fmt.Sprintf("seaweedfs-%s", t.Component.GetType())
	serviceContent := t.generateSystemdService(serviceName)
	servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)

	cmd := fmt.Sprintf("sudo bash -c 'cat > %s << \"EOF\"\n%s\nEOF'", servicePath, serviceContent)
	if _, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd); err != nil {
		return err
	}

	// Reload systemd
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), "sudo systemctl daemon-reload")
	return err
}

// generateSystemdService creates systemd service content
func (t *DeployComponentTask) generateSystemdService(serviceName string) string {
	return fmt.Sprintf(`[Unit]
Description=SeaweedFS %s
After=network.target

[Service]
Type=simple
User=seaweed
Group=seaweed
ExecStart=%s/weed %s -port=%d -dir=%s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, t.Component.GetType(), t.ConfigDir, t.Component.GetType(), t.Component.GetPort(), t.Component.GetDataDir())
}

// startService starts the systemd service
func (t *DeployComponentTask) startService(ctx context.Context) error {
	serviceName := fmt.Sprintf("seaweedfs-%s", t.Component.GetType())

	// Enable and start service
	cmd := fmt.Sprintf("sudo systemctl enable %s && sudo systemctl start %s", serviceName, serviceName)
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

// stopService stops the systemd service
func (t *DeployComponentTask) stopService(ctx context.Context) error {
	serviceName := fmt.Sprintf("seaweedfs-%s", t.Component.GetType())
	cmd := fmt.Sprintf("sudo systemctl stop %s", serviceName)

	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

// removeSystemdService removes the systemd service
func (t *DeployComponentTask) removeSystemdService(ctx context.Context) error {
	serviceName := fmt.Sprintf("seaweedfs-%s", t.Component.GetType())
	servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)

	cmd := fmt.Sprintf("sudo systemctl disable %s && sudo rm -f %s && sudo systemctl daemon-reload", serviceName, servicePath)
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

// waitForHealthy waits for the component to become healthy
func (t *DeployComponentTask) waitForHealthy(ctx context.Context) error {
	// Create a status collector to check health
	collector := status.NewStatusCollector(t.Executor)

	// Wait up to 60 seconds for the service to become healthy
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("component did not become healthy within 60 seconds")
		case <-ticker.C:
			// Check if the service is healthy
			// This is a simplified check - in reality, you'd check the specific component
			if t.isComponentHealthy(ctx, collector) {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// isComponentHealthy checks if the component is healthy
func (t *DeployComponentTask) isComponentHealthy(ctx context.Context, collector *status.StatusCollector) bool {
	// Simplified health check - just check if the process is running
	cmd := fmt.Sprintf("systemctl is-active seaweedfs-%s", t.Component.GetType())
	output, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)

	return err == nil && output == "active"
}

// UpgradeComponentTask upgrades a component to a new version
type UpgradeComponentTask struct {
	BaseTask
	Component       spec.ComponentSpec
	CurrentVersion  string
	TargetVersion   string
	Executor        executor.Executor
	Registry        *registry.ComponentRegistry
	StatusCollector *status.StatusCollector
}

// Execute performs the component upgrade
func (t *UpgradeComponentTask) Execute(ctx context.Context) error {
	// Pre-upgrade health check
	if !t.isComponentHealthy(ctx) {
		return fmt.Errorf("component is not healthy before upgrade")
	}

	// Backup current configuration
	if err := t.backupConfig(ctx); err != nil {
		return fmt.Errorf("failed to backup configuration: %w", err)
	}

	// Stop the current service gracefully
	if err := t.stopService(ctx); err != nil {
		return fmt.Errorf("failed to stop service: %w", err)
	}

	// Deploy new binary
	if err := t.deployNewBinary(ctx); err != nil {
		return fmt.Errorf("failed to deploy new binary: %w", err)
	}

	// Start service with new binary
	if err := t.startService(ctx); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	// Wait for service to be healthy
	if err := t.waitForHealthy(ctx); err != nil {
		return fmt.Errorf("service did not become healthy after upgrade: %w", err)
	}

	// Post-upgrade verification
	return t.verifyUpgrade(ctx)
}

// Rollback reverts the upgrade
func (t *UpgradeComponentTask) Rollback(ctx context.Context) error {
	// Stop current service
	t.stopService(ctx)

	// Restore previous binary
	if err := t.restorePreviousBinary(ctx); err != nil {
		return err
	}

	// Restore configuration
	if err := t.restoreConfig(ctx); err != nil {
		return err
	}

	// Start service
	if err := t.startService(ctx); err != nil {
		return err
	}

	// Wait for health
	return t.waitForHealthy(ctx)
}

// Helper methods for UpgradeComponentTask
func (t *UpgradeComponentTask) isComponentHealthy(ctx context.Context) bool {
	cmd := fmt.Sprintf("systemctl is-active seaweedfs-%s", t.Component.GetType())
	output, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err == nil && output == "active"
}

func (t *UpgradeComponentTask) backupConfig(ctx context.Context) error {
	configDir := "/etc/seaweedfs" // TODO: Make configurable
	backupDir := fmt.Sprintf("/tmp/seaweedfs-backup-%d", time.Now().Unix())

	cmd := fmt.Sprintf("cp -r %s %s", configDir, backupDir)
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

func (t *UpgradeComponentTask) stopService(ctx context.Context) error {
	serviceName := fmt.Sprintf("seaweedfs-%s", t.Component.GetType())
	cmd := fmt.Sprintf("sudo systemctl stop %s", serviceName)
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

func (t *UpgradeComponentTask) deployNewBinary(ctx context.Context) error {
	// Get new binary path
	binaryPath, err := t.Registry.GetBinaryPath("seaweedfs", t.TargetVersion)
	if err != nil {
		return err
	}

	// Backup current binary
	configDir := "/etc/seaweedfs" // TODO: Make configurable
	cmd := fmt.Sprintf("cp %s/weed %s/weed.backup", configDir, configDir)
	t.Executor.Execute(ctx, t.Component.GetHost(), cmd) // Don't fail if backup fails

	// Copy new binary (placeholder)
	cmd = fmt.Sprintf("echo 'Deploy new binary %s to %s/weed'", binaryPath, configDir)
	_, err = t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

func (t *UpgradeComponentTask) startService(ctx context.Context) error {
	serviceName := fmt.Sprintf("seaweedfs-%s", t.Component.GetType())
	cmd := fmt.Sprintf("sudo systemctl start %s", serviceName)
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

func (t *UpgradeComponentTask) waitForHealthy(ctx context.Context) error {
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("component did not become healthy within 60 seconds")
		case <-ticker.C:
			if t.isComponentHealthy(ctx) {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *UpgradeComponentTask) verifyUpgrade(ctx context.Context) error {
	// Verify the version
	cmd := "/etc/seaweedfs/weed version" // TODO: Make configurable
	output, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	if err != nil {
		return err
	}

	// Simple version check (in reality, you'd parse the output)
	if output == "" {
		return fmt.Errorf("unable to verify version")
	}

	return nil
}

func (t *UpgradeComponentTask) restorePreviousBinary(ctx context.Context) error {
	configDir := "/etc/seaweedfs" // TODO: Make configurable
	cmd := fmt.Sprintf("cp %s/weed.backup %s/weed", configDir, configDir)
	_, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

func (t *UpgradeComponentTask) restoreConfig(ctx context.Context) error {
	// Find the latest backup
	cmd := "ls -t /tmp/seaweedfs-backup-* | head -1"
	backupDir, err := t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	if err != nil {
		return err
	}

	// Restore configuration
	configDir := "/etc/seaweedfs" // TODO: Make configurable
	cmd = fmt.Sprintf("cp -r %s/* %s/", backupDir, configDir)
	_, err = t.Executor.Execute(ctx, t.Component.GetHost(), cmd)
	return err
}

// ScaleOutTask adds new components to the cluster
type ScaleOutTask struct {
	BaseTask
	NewComponents []spec.ComponentSpec
	Version       string
	Executor      executor.Executor
	Registry      *registry.ComponentRegistry
}

// Execute adds new components
func (t *ScaleOutTask) Execute(ctx context.Context) error {
	for _, component := range t.NewComponents {
		deployTask := &DeployComponentTask{
			BaseTask: BaseTask{
				ID:          fmt.Sprintf("deploy-%s-%s", component.GetType(), component.GetHost()),
				Name:        fmt.Sprintf("Deploy %s", component.GetType()),
				Description: fmt.Sprintf("Deploy %s on %s:%d", component.GetType(), component.GetHost(), component.GetPort()),
			},
			Component: component,
			Version:   t.Version,
			ConfigDir: "/etc/seaweedfs",
			DataDir:   component.GetDataDir(),
			Executor:  t.Executor,
			Registry:  t.Registry,
		}

		if err := deployTask.Execute(ctx); err != nil {
			return fmt.Errorf("failed to deploy component %s on %s: %w", component.GetType(), component.GetHost(), err)
		}
	}

	return nil
}

// Rollback removes the newly added components
func (t *ScaleOutTask) Rollback(ctx context.Context) error {
	// Remove components in reverse order
	for i := len(t.NewComponents) - 1; i >= 0; i-- {
		component := t.NewComponents[i]

		// Stop and remove the component
		serviceName := fmt.Sprintf("seaweedfs-%s", component.GetType())
		cmd := fmt.Sprintf("sudo systemctl stop %s && sudo systemctl disable %s", serviceName, serviceName)
		t.Executor.Execute(ctx, component.GetHost(), cmd) // Don't fail rollback if this fails
	}

	return nil
}

// Implement ComponentSpec interface for existing spec types
type MasterComponentSpec struct {
	*spec.MasterServerSpec
}

func (m *MasterComponentSpec) GetHost() string { return m.Host }
func (m *MasterComponentSpec) GetPort() int    { return m.Port }
func (m *MasterComponentSpec) GetType() string { return "master" }
func (m *MasterComponentSpec) GetDataDir() string {
	if m.DataDir != "" {
		return m.DataDir
	}
	return "/opt/seaweedfs/master"
}

type VolumeComponentSpec struct {
	*spec.VolumeServerSpec
}

func (v *VolumeComponentSpec) GetHost() string { return v.Host }
func (v *VolumeComponentSpec) GetPort() int    { return v.Port }
func (v *VolumeComponentSpec) GetType() string { return "volume" }
func (v *VolumeComponentSpec) GetDataDir() string {
	if v.DataDir != "" {
		return v.DataDir
	}
	return "/opt/seaweedfs/volume"
}

type FilerComponentSpec struct {
	*spec.FilerServerSpec
}

func (f *FilerComponentSpec) GetHost() string { return f.Host }
func (f *FilerComponentSpec) GetPort() int    { return f.Port }
func (f *FilerComponentSpec) GetType() string { return "filer" }
func (f *FilerComponentSpec) GetDataDir() string {
	if f.DataDir != "" {
		return f.DataDir
	}
	return "/opt/seaweedfs/filer"
}
