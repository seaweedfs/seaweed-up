package operation

import (
	"context"
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/executor"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/status"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/task"
	"github.com/seaweedfs/seaweed-up/pkg/component/registry"
	"github.com/seaweedfs/seaweed-up/pkg/component/repository"
)

// ClusterOperationManager manages cluster operations
type ClusterOperationManager struct {
	executor        executor.Executor
	registry        *registry.ComponentRegistry
	repository      *repository.GitHubRepository
	statusCollector *status.StatusCollector
}

// NewClusterOperationManager creates a new cluster operation manager
func NewClusterOperationManager(
	exec executor.Executor,
	reg *registry.ComponentRegistry,
	repo *repository.GitHubRepository,
) *ClusterOperationManager {
	return &ClusterOperationManager{
		executor:        exec,
		registry:        reg,
		repository:      repo,
		statusCollector: status.NewStatusCollector(exec),
	}
}

// DeployCluster deploys a complete SeaweedFS cluster
func (com *ClusterOperationManager) DeployCluster(ctx context.Context, cluster *spec.Specification, version string, dryRun bool) error {
	color.Green("🚀 Starting cluster deployment: %s", cluster.Name)

	if dryRun {
		color.Yellow("🔍 DRY RUN MODE - No actual changes will be made")
		return com.printDeploymentPlan(cluster, version)
	}

	// Validate version
	if version == "" || version == "latest" {
		var err error
		version, err = com.repository.GetLatestVersion(ctx)
		if err != nil {
			return fmt.Errorf("failed to get latest version: %w", err)
		}
	}

	// Ensure components are installed locally
	if !com.registry.IsInstalled("seaweedfs", version) {
		color.Cyan("📦 Installing SeaweedFS version %s...", version)
		if _, err := com.repository.DownloadComponent(ctx, version, true); err != nil {
			return fmt.Errorf("failed to download component: %w", err)
		}
	}

	// Create deployment orchestrator
	orchestrator := task.NewTaskOrchestrator()

	// Phase 1: Deploy Master Servers
	if len(cluster.MasterServers) > 0 {
		masterGroup := task.NewTaskGroup("deploy-masters", "Deploy Master Servers", false)
		masterGroup.ContinueOnError = false

		for i, masterSpec := range cluster.MasterServers {
			deployTask := &task.DeployComponentTask{
				BaseTask: task.BaseTask{
					ID:          fmt.Sprintf("deploy-master-%d", i),
					Name:        fmt.Sprintf("Deploy Master %d", i+1),
					Description: fmt.Sprintf("Deploy master on %s:%d", masterSpec.Host, masterSpec.Port),
				},
				Component: &task.MasterComponentSpec{MasterServerSpec: masterSpec},
				Version:   version,
				ConfigDir: "/etc/seaweedfs",
				DataDir:   masterSpec.DataDir,
				Executor:  com.executor,
				Registry:  com.registry,
			}
			masterGroup.AddTask(deployTask)
		}

		orchestrator.AddTaskGroup(masterGroup)
	}

	// Phase 2: Deploy Volume Servers
	if len(cluster.VolumeServers) > 0 {
		volumeGroup := task.NewTaskGroup("deploy-volumes", "Deploy Volume Servers", true) // Can be parallel
		volumeGroup.ContinueOnError = false

		for i, volumeSpec := range cluster.VolumeServers {
			deployTask := &task.DeployComponentTask{
				BaseTask: task.BaseTask{
					ID:          fmt.Sprintf("deploy-volume-%d", i),
					Name:        fmt.Sprintf("Deploy Volume %d", i+1),
					Description: fmt.Sprintf("Deploy volume on %s:%d", volumeSpec.Host, volumeSpec.Port),
				},
				Component: &task.VolumeComponentSpec{VolumeServerSpec: volumeSpec},
				Version:   version,
				ConfigDir: "/etc/seaweedfs",
				DataDir:   volumeSpec.DataDir,
				Executor:  com.executor,
				Registry:  com.registry,
			}
			volumeGroup.AddTask(deployTask)
		}

		orchestrator.AddTaskGroup(volumeGroup)
	}

	// Phase 3: Deploy Filer Servers
	if len(cluster.FilerServers) > 0 {
		filerGroup := task.NewTaskGroup("deploy-filers", "Deploy Filer Servers", true) // Can be parallel
		filerGroup.ContinueOnError = false

		for i, filerSpec := range cluster.FilerServers {
			deployTask := &task.DeployComponentTask{
				BaseTask: task.BaseTask{
					ID:          fmt.Sprintf("deploy-filer-%d", i),
					Name:        fmt.Sprintf("Deploy Filer %d", i+1),
					Description: fmt.Sprintf("Deploy filer on %s:%d", filerSpec.Host, filerSpec.Port),
				},
				Component: &task.FilerComponentSpec{FilerServerSpec: filerSpec},
				Version:   version,
				ConfigDir: "/etc/seaweedfs",
				DataDir:   filerSpec.DataDir,
				Executor:  com.executor,
				Registry:  com.registry,
			}
			filerGroup.AddTask(deployTask)
		}

		orchestrator.AddTaskGroup(filerGroup)
	}

	// Execute deployment
	startTime := time.Now()
	err := orchestrator.Execute(ctx)
	duration := time.Since(startTime)

	if err != nil {
		color.Red("❌ Deployment failed after %.2f seconds: %v", duration.Seconds(), err)
		return err
	}

	color.Green("🎉 Cluster deployment completed successfully in %.2f seconds!", duration.Seconds())

	// Verify deployment
	return com.verifyClusterDeployment(ctx, cluster)
}

// UpgradeCluster performs a rolling upgrade of the cluster
func (com *ClusterOperationManager) UpgradeCluster(ctx context.Context, cluster *spec.Specification, targetVersion string, dryRun bool) error {
	color.Green("⬆️  Starting cluster upgrade: %s -> %s", cluster.Name, targetVersion)

	if dryRun {
		color.Yellow("🔍 DRY RUN MODE - No actual changes will be made")
		return com.printUpgradePlan(cluster, targetVersion)
	}

	// Pre-upgrade validation
	if err := com.validateUpgrade(ctx, cluster, targetVersion); err != nil {
		return fmt.Errorf("upgrade validation failed: %w", err)
	}

	// Ensure target version is available
	if !com.registry.IsInstalled("seaweedfs", targetVersion) {
		color.Cyan("📦 Installing SeaweedFS version %s...", targetVersion)
		if _, err := com.repository.DownloadComponent(ctx, targetVersion, true); err != nil {
			return fmt.Errorf("failed to download target version: %w", err)
		}
	}

	// Create upgrade orchestrator
	orchestrator := task.NewTaskOrchestrator()

	// Phase 1: Upgrade Master Servers (sequential for safety)
	if len(cluster.MasterServers) > 0 {
		masterGroup := task.NewTaskGroup("upgrade-masters", "Upgrade Master Servers", false)
		masterGroup.ContinueOnError = false

		for i, masterSpec := range cluster.MasterServers {
			upgradeTask := &task.UpgradeComponentTask{
				BaseTask: task.BaseTask{
					ID:          fmt.Sprintf("upgrade-master-%d", i),
					Name:        fmt.Sprintf("Upgrade Master %d", i+1),
					Description: fmt.Sprintf("Upgrade master on %s:%d", masterSpec.Host, masterSpec.Port),
				},
				Component:       &task.MasterComponentSpec{MasterServerSpec: masterSpec},
				CurrentVersion:  "current", // TODO: Get actual current version
				TargetVersion:   targetVersion,
				Executor:        com.executor,
				Registry:        com.registry,
				StatusCollector: com.statusCollector,
			}
			masterGroup.AddTask(upgradeTask)
		}

		orchestrator.AddTaskGroup(masterGroup)
	}

	// Phase 2: Upgrade Volume Servers (can be parallel with rolling strategy)
	if len(cluster.VolumeServers) > 0 {
		volumeGroup := task.NewTaskGroup("upgrade-volumes", "Upgrade Volume Servers", false) // Sequential for safety
		volumeGroup.ContinueOnError = false

		for i, volumeSpec := range cluster.VolumeServers {
			upgradeTask := &task.UpgradeComponentTask{
				BaseTask: task.BaseTask{
					ID:          fmt.Sprintf("upgrade-volume-%d", i),
					Name:        fmt.Sprintf("Upgrade Volume %d", i+1),
					Description: fmt.Sprintf("Upgrade volume on %s:%d", volumeSpec.Host, volumeSpec.Port),
				},
				Component:       &task.VolumeComponentSpec{VolumeServerSpec: volumeSpec},
				CurrentVersion:  "current", // TODO: Get actual current version
				TargetVersion:   targetVersion,
				Executor:        com.executor,
				Registry:        com.registry,
				StatusCollector: com.statusCollector,
			}
			volumeGroup.AddTask(upgradeTask)
		}

		orchestrator.AddTaskGroup(volumeGroup)
	}

	// Phase 3: Upgrade Filer Servers
	if len(cluster.FilerServers) > 0 {
		filerGroup := task.NewTaskGroup("upgrade-filers", "Upgrade Filer Servers", true) // Can be parallel
		filerGroup.ContinueOnError = false

		for i, filerSpec := range cluster.FilerServers {
			upgradeTask := &task.UpgradeComponentTask{
				BaseTask: task.BaseTask{
					ID:          fmt.Sprintf("upgrade-filer-%d", i),
					Name:        fmt.Sprintf("Upgrade Filer %d", i+1),
					Description: fmt.Sprintf("Upgrade filer on %s:%d", filerSpec.Host, filerSpec.Port),
				},
				Component:       &task.FilerComponentSpec{FilerServerSpec: filerSpec},
				CurrentVersion:  "current", // TODO: Get actual current version
				TargetVersion:   targetVersion,
				Executor:        com.executor,
				Registry:        com.registry,
				StatusCollector: com.statusCollector,
			}
			filerGroup.AddTask(upgradeTask)
		}

		orchestrator.AddTaskGroup(filerGroup)
	}

	// Execute upgrade
	startTime := time.Now()
	err := orchestrator.Execute(ctx)
	duration := time.Since(startTime)

	if err != nil {
		color.Red("❌ Upgrade failed after %.2f seconds: %v", duration.Seconds(), err)
		return err
	}

	color.Green("🎉 Cluster upgrade completed successfully in %.2f seconds!", duration.Seconds())

	// Post-upgrade verification
	return com.verifyClusterUpgrade(ctx, cluster, targetVersion)
}

// ScaleOut adds new components to the cluster
func (com *ClusterOperationManager) ScaleOut(ctx context.Context, cluster *spec.Specification, scaleConfig ScaleOutConfig, dryRun bool) error {
	color.Green("📈 Starting cluster scale-out: %s", cluster.Name)

	if dryRun {
		color.Yellow("🔍 DRY RUN MODE - No actual changes will be made")
		return com.printScaleOutPlan(cluster, scaleConfig)
	}

	// Validate scale-out configuration
	if err := com.validateScaleOut(ctx, cluster, scaleConfig); err != nil {
		return fmt.Errorf("scale-out validation failed: %w", err)
	}

	// Get current version from cluster
	version := scaleConfig.Version
	if version == "" {
		// TODO: Detect current cluster version
		version = "latest"
	}

	// Ensure version is available
	if !com.registry.IsInstalled("seaweedfs", version) {
		color.Cyan("📦 Installing SeaweedFS version %s...", version)
		if _, err := com.repository.DownloadComponent(ctx, version, true); err != nil {
			return fmt.Errorf("failed to download version: %w", err)
		}
	}

	// Create scale-out task
	var newComponents []spec.ComponentSpec

	// Add new volume servers
	for _, volumeSpec := range scaleConfig.NewVolumeServers {
		newComponents = append(newComponents, &task.VolumeComponentSpec{VolumeServerSpec: volumeSpec})
	}

	// Add new filer servers
	for _, filerSpec := range scaleConfig.NewFilerServers {
		newComponents = append(newComponents, &task.FilerComponentSpec{FilerServerSpec: filerSpec})
	}

	if len(newComponents) == 0 {
		return fmt.Errorf("no new components specified for scale-out")
	}

	scaleOutTask := &task.ScaleOutTask{
		BaseTask: task.BaseTask{
			ID:          "scale-out",
			Name:        "Scale Out Cluster",
			Description: fmt.Sprintf("Add %d new components to cluster", len(newComponents)),
		},
		NewComponents: newComponents,
		Version:       version,
		Executor:      com.executor,
		Registry:      com.registry,
	}

	// Execute scale-out
	startTime := time.Now()
	err := scaleOutTask.Execute(ctx)
	duration := time.Since(startTime)

	if err != nil {
		color.Red("❌ Scale-out failed after %.2f seconds: %v", duration.Seconds(), err)
		return err
	}

	color.Green("🎉 Cluster scale-out completed successfully in %.2f seconds!", duration.Seconds())

	// Verify scale-out
	return com.verifyScaleOut(ctx, cluster, scaleConfig)
}

// ScaleOutConfig defines scale-out configuration
type ScaleOutConfig struct {
	NewVolumeServers []*spec.VolumeServerSpec `json:"new_volume_servers,omitempty"`
	NewFilerServers  []*spec.FilerServerSpec  `json:"new_filer_servers,omitempty"`
	Version          string                   `json:"version,omitempty"`
}

// Helper methods for validation and verification

func (com *ClusterOperationManager) printDeploymentPlan(cluster *spec.Specification, version string) error {
	color.Cyan("📋 Deployment Plan for %s", cluster.Name)
	fmt.Printf("Version: %s\n", version)
	fmt.Printf("Master Servers: %d\n", len(cluster.MasterServers))
	fmt.Printf("Volume Servers: %d\n", len(cluster.VolumeServers))
	fmt.Printf("Filer Servers: %d\n", len(cluster.FilerServers))
	return nil
}

func (com *ClusterOperationManager) printUpgradePlan(cluster *spec.Specification, targetVersion string) error {
	color.Cyan("📋 Upgrade Plan for %s", cluster.Name)
	fmt.Printf("Target Version: %s\n", targetVersion)
	fmt.Printf("Components to upgrade: %d\n", len(cluster.MasterServers)+len(cluster.VolumeServers)+len(cluster.FilerServers))
	return nil
}

func (com *ClusterOperationManager) printScaleOutPlan(cluster *spec.Specification, config ScaleOutConfig) error {
	color.Cyan("📋 Scale-out Plan for %s", cluster.Name)
	fmt.Printf("New Volume Servers: %d\n", len(config.NewVolumeServers))
	fmt.Printf("New Filer Servers: %d\n", len(config.NewFilerServers))
	return nil
}

func (com *ClusterOperationManager) validateUpgrade(ctx context.Context, cluster *spec.Specification, targetVersion string) error {
	// Check if target version exists
	versions, err := com.repository.ListVersions(ctx)
	if err != nil {
		return err
	}

	found := false
	for _, version := range versions {
		if version == targetVersion {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("target version %s not found", targetVersion)
	}

	// Check cluster health before upgrade
	statusOpts := status.StatusCollectionOptions{
		Timeout:        30 * time.Second,
		IncludeMetrics: false,
		HealthCheck:    true,
	}

	clusterStatus, err := com.statusCollector.CollectClusterStatus(ctx, cluster, statusOpts)
	if err != nil {
		color.Yellow("⚠️  Could not collect cluster status for pre-upgrade validation: %v", err)
		// Don't fail validation, just warn
		return nil
	}

	if clusterStatus.State != status.StateRunning {
		return fmt.Errorf("cluster is not in running state: %s", clusterStatus.State)
	}

	return nil
}

func (com *ClusterOperationManager) validateScaleOut(ctx context.Context, cluster *spec.Specification, config ScaleOutConfig) error {
	// Validate that new components don't conflict with existing ones
	existingPorts := make(map[string]bool)

	// Collect existing ports
	for _, master := range cluster.MasterServers {
		key := fmt.Sprintf("%s:%d", master.Host, master.Port)
		existingPorts[key] = true
	}

	for _, volume := range cluster.VolumeServers {
		key := fmt.Sprintf("%s:%d", volume.Host, volume.Port)
		existingPorts[key] = true
	}

	for _, filer := range cluster.FilerServers {
		key := fmt.Sprintf("%s:%d", filer.Host, filer.Port)
		existingPorts[key] = true
	}

	// Check new volume servers
	for _, volume := range config.NewVolumeServers {
		key := fmt.Sprintf("%s:%d", volume.Host, volume.Port)
		if existingPorts[key] {
			return fmt.Errorf("port conflict: %s already in use", key)
		}
	}

	// Check new filer servers
	for _, filer := range config.NewFilerServers {
		key := fmt.Sprintf("%s:%d", filer.Host, filer.Port)
		if existingPorts[key] {
			return fmt.Errorf("port conflict: %s already in use", key)
		}
	}

	return nil
}

func (com *ClusterOperationManager) verifyClusterDeployment(ctx context.Context, cluster *spec.Specification) error {
	color.Cyan("🔍 Verifying cluster deployment...")

	statusOpts := status.StatusCollectionOptions{
		Timeout:        30 * time.Second,
		IncludeMetrics: false,
		HealthCheck:    true,
	}

	clusterStatus, err := com.statusCollector.CollectClusterStatus(ctx, cluster, statusOpts)
	if err != nil {
		color.Yellow("⚠️  Could not verify deployment: %v", err)
		return nil // Don't fail deployment verification
	}

	healthyComponents := 0
	for _, comp := range clusterStatus.Components {
		if comp.HealthCheck.Status == "healthy" {
			healthyComponents++
		}
	}

	totalComponents := len(clusterStatus.Components)
	if healthyComponents == totalComponents {
		color.Green("✅ All %d components are healthy", totalComponents)
	} else {
		color.Yellow("⚠️  %d/%d components are healthy", healthyComponents, totalComponents)
	}

	return nil
}

func (com *ClusterOperationManager) verifyClusterUpgrade(ctx context.Context, cluster *spec.Specification, targetVersion string) error {
	color.Cyan("🔍 Verifying cluster upgrade...")
	// TODO: Verify that all components are running the target version
	return nil
}

func (com *ClusterOperationManager) verifyScaleOut(ctx context.Context, cluster *spec.Specification, config ScaleOutConfig) error {
	color.Cyan("🔍 Verifying scale-out...")
	// TODO: Verify that new components are healthy and integrated
	return nil
}
