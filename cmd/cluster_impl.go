package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	
	"github.com/seaweedfs/seaweed-up/pkg/cluster/executor"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/status"
	"github.com/seaweedfs/seaweed-up/pkg/config"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"gopkg.in/yaml.v3"
)

// Option structs for cluster commands
type ClusterDeployOptions struct {
	ConfigFile   string
	User         string
	SSHPort      int
	IdentityFile string
	Version      string
	Component    string
	MountDisks   bool
	ForceRestart bool
	ProxyUrl     string
	SkipConfirm  bool
}

type ClusterStatusOptions struct {
	JSONOutput bool
	Verbose    bool
	Timeout    string
	Refresh    int
}

type ClusterUpgradeOptions struct {
	Version     string
	ConfigFile  string
	SkipConfirm bool
	DryRun      bool
}

type ClusterScaleOutOptions struct {
	ConfigFile  string
	AddVolume   int
	AddFiler    int
	SkipConfirm bool
}

type ClusterScaleInOptions struct {
	ConfigFile  string
	RemoveNodes []string
	SkipConfirm bool
}

type ClusterDestroyOptions struct {
	ConfigFile  string
	SkipConfirm bool
	RemoveData  bool
}

type ClusterListOptions struct {
	JSONOutput bool
	Verbose    bool
}

func runClusterDeploy(args []string, opts *ClusterDeployOptions) error {
	color.Green("🚀 Deploying SeaweedFS cluster...")
	
	// Load cluster specification
	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	
	// Set cluster name from args if provided
	if len(args) > 0 {
		clusterSpec.Name = args[0]
	}
	
	// Create cluster manager
	mgr := manager.NewManager()
	mgr.User = opts.User
	mgr.SshPort = opts.SSHPort
	mgr.IdentityFile = opts.IdentityFile
	mgr.Version = opts.Version
	mgr.ComponentToDeploy = opts.Component
	mgr.PrepareVolumeDisks = opts.MountDisks
	mgr.ForceRestart = opts.ForceRestart
	mgr.ProxyUrl = opts.ProxyUrl
	
	// Get latest version if not specified
	if mgr.Version == "" {
		latest, err := config.GitHubLatestRelease(context.Background(), "0", "seaweedfs", "seaweedfs")
		if err != nil {
			return fmt.Errorf("unable to get latest version: %w", err)
		}
		mgr.Version = latest.Version
	}
	
	// Confirm deployment if not skipped
	if !opts.SkipConfirm {
		color.Yellow("📋 Deployment Summary:")
		fmt.Printf("  Cluster: %s\n", clusterSpec.Name)
		fmt.Printf("  Version: %s\n", mgr.Version)
		fmt.Printf("  Masters: %d\n", len(clusterSpec.MasterServers))
		fmt.Printf("  Volumes: %d\n", len(clusterSpec.VolumeServers))
		fmt.Printf("  Filers:  %d\n", len(clusterSpec.FilerServers))
		
		if !utils.PromptForConfirmation("Proceed with deployment?") {
			color.Yellow("⚠️  Deployment cancelled by user")
			return nil
		}
	}
	
	// Deploy cluster
	if err := mgr.DeployCluster(clusterSpec); err != nil {
		color.Red("❌ Deployment failed: %v", err)
		return err
	}
	
	color.Green("✅ Cluster deployed successfully!")
	color.Cyan("💡 Next steps:")
	fmt.Println("  - Check cluster status: seaweed-up cluster status", clusterSpec.Name)
	fmt.Println("  - View logs: seaweed-up cluster logs", clusterSpec.Name)
	
	return nil
}

func runClusterStatus(args []string, opts *ClusterStatusOptions) error {
	if opts.Refresh > 0 {
		return runClusterStatusWithRefresh(args, opts)
	}
	
	color.Green("📊 Cluster Status")
	
	if len(args) == 0 {
		// Show all clusters (for now, just show help message)
		color.Yellow("📋 All Clusters:")
		fmt.Println("No clusters found. Deploy a cluster first with 'seaweed-up cluster deploy'")
		color.Cyan("💡 Usage: seaweed-up cluster status <cluster-name> -f <config-file>")
		return nil
	}
	
	// For now, we need a config file to know what to monitor
	// In the future, we'll maintain a registry of deployed clusters
	if len(args) == 0 {
		return fmt.Errorf("cluster name is required")
	}
	
	clusterName := args[0]
	color.Yellow("📋 Cluster: %s", clusterName)
	
	// For demonstration, create a sample cluster spec
	// In a real implementation, this would be loaded from the cluster registry
	sampleCluster := &spec.Specification{
		Name: clusterName,
		MasterServers: []*spec.MasterServerSpec{
			{Host: "localhost", Port: 9333},
		},
		VolumeServers: []*spec.VolumeServerSpec{
			{Host: "localhost", Port: 8382},
		},
		FilerServers: []*spec.FilerServerSpec{
			{Host: "localhost", Port: 8888},
		},
	}
	
	// Create status collector with local executor (for demo)
	localExecutor := executor.NewLocalExecutor()
	defer localExecutor.Close()
	
	collector := status.NewStatusCollector(localExecutor)
	
	// Parse timeout
	timeout, err := time.ParseDuration(opts.Timeout)
	if err != nil {
		timeout = 30 * time.Second
	}
	
	// Collect status
	statusOpts := status.StatusCollectionOptions{
		Timeout:        timeout,
		IncludeMetrics: opts.Verbose,
		Verbose:        opts.Verbose,
		HealthCheck:    true,
	}
	
	clusterStatus, err := collector.CollectClusterStatus(context.Background(), sampleCluster, statusOpts)
	if err != nil {
		return fmt.Errorf("failed to collect cluster status: %w", err)
	}
	
	// Output results
	if opts.JSONOutput {
		return outputClusterStatusJSON(clusterStatus)
	}
	
	return displayClusterStatus(clusterStatus, opts.Verbose)
}

func runClusterStatusWithRefresh(args []string, opts *ClusterStatusOptions) error {
	ticker := time.NewTicker(time.Duration(opts.Refresh) * time.Second)
	defer ticker.Stop()
	
	for {
		// Clear screen
		fmt.Print("\033[H\033[2J")
		
		// Show status
		if err := runClusterStatus(args, &ClusterStatusOptions{
			JSONOutput: opts.JSONOutput,
			Verbose:    opts.Verbose,
			Timeout:    opts.Timeout,
		}); err != nil {
			return err
		}
		
		color.Cyan("🔄 Refreshing every %d seconds (Press Ctrl+C to stop)", opts.Refresh)
		<-ticker.C
	}
}

func runClusterUpgrade(clusterName string, opts *ClusterUpgradeOptions) error {
	color.Green("⬆️  Upgrading cluster: %s to version %s", clusterName, opts.Version)
	
	if opts.DryRun {
		color.Yellow("🔍 Dry run mode - no changes will be made")
	}
	
	// TODO: Implement upgrade logic
	fmt.Println("Upgrade functionality not yet implemented")
	
	return nil
}

func runClusterScaleOut(clusterName string, opts *ClusterScaleOutOptions) error {
	color.Green("📈 Scaling out cluster: %s", clusterName)
	
	if opts.AddVolume > 0 {
		fmt.Printf("Adding %d volume servers\n", opts.AddVolume)
	}
	if opts.AddFiler > 0 {
		fmt.Printf("Adding %d filer servers\n", opts.AddFiler)
	}
	
	// TODO: Implement scale out logic
	fmt.Println("Scale out functionality not yet implemented")
	
	return nil
}

func runClusterScaleIn(clusterName string, opts *ClusterScaleInOptions) error {
	color.Green("📉 Scaling in cluster: %s", clusterName)
	
	if len(opts.RemoveNodes) > 0 {
		fmt.Printf("Removing nodes: %v\n", opts.RemoveNodes)
	}
	
	// TODO: Implement scale in logic
	fmt.Println("Scale in functionality not yet implemented")
	
	return nil
}

func runClusterDestroy(clusterName string, opts *ClusterDestroyOptions) error {
	color.Red("💥 WARNING: This will destroy cluster '%s'", clusterName)
	
	if opts.RemoveData {
		color.Red("⚠️  ALL DATA WILL BE PERMANENTLY DELETED!")
	}
	
	if !opts.SkipConfirm {
		color.Yellow("Type the cluster name to confirm destruction:")
		var confirmation string
		fmt.Scanln(&confirmation)
		
		if confirmation != clusterName {
			color.Yellow("⚠️  Destruction cancelled - cluster name didn't match")
			return nil
		}
	}
	
	// TODO: Implement destroy logic
	fmt.Printf("Destroy functionality not yet implemented\n")
	
	return nil
}

func runClusterList(opts *ClusterListOptions) error {
	color.Green("📋 Managed Clusters")
	
	// TODO: Implement cluster listing
	fmt.Println("No clusters found. Deploy a cluster first with 'seaweed-up cluster deploy'")
	
	return nil
}

func loadClusterSpec(configFile string) (*spec.Specification, error) {
	if configFile == "" {
		return nil, fmt.Errorf("configuration file is required")
	}
	
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configFile, err)
	}
	
	clusterSpec := &spec.Specification{}
	if err := yaml.Unmarshal(data, clusterSpec); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configFile, err)
	}
	
	return clusterSpec, nil
}

// Helper functions for status display

func outputClusterStatusJSON(clusterStatus *status.ClusterStatus) error {
	data, err := json.MarshalIndent(clusterStatus, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal status to JSON: %w", err)
	}
	
	fmt.Println(string(data))
	return nil
}

func displayClusterStatus(clusterStatus *status.ClusterStatus, verbose bool) error {
	// Display cluster summary
	color.Green("🏗️  Cluster: %s", clusterStatus.Name)
	fmt.Printf("State: %s\n", getStateIcon(clusterStatus.State)+string(clusterStatus.State))
	fmt.Printf("Components: %d\n", len(clusterStatus.Components))
	
	if clusterStatus.Version != "" {
		fmt.Printf("Version: %s\n", clusterStatus.Version)
	}
	
	fmt.Printf("Last Updated: %s\n", clusterStatus.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Println()
	
	// Create components table
	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	
	if verbose {
		t.AppendHeader(table.Row{"Component", "Type", "Host:Port", "Status", "Health", "PID", "Memory", "Uptime", "Last Check"})
	} else {
		t.AppendHeader(table.Row{"Component", "Type", "Host:Port", "Status", "Health"})
	}
	
	for _, comp := range clusterStatus.Components {
		healthIcon := getHealthIcon(comp.HealthCheck.Status)
		statusIcon := getStatusIcon(comp.Status)
		
		hostPort := fmt.Sprintf("%s:%d", comp.Host, comp.Port)
		
		if verbose {
			memory := "N/A"
			if comp.MemoryUsage > 0 {
				memory = utils.FormatBytes(comp.MemoryUsage)
			}
			
			uptime := "N/A"
			if comp.Uptime > 0 {
				uptime = utils.FormatDuration(int64(comp.Uptime.Seconds()))
			}
			
			lastCheck := "N/A"
			if !comp.HealthCheck.LastCheck.IsZero() {
				lastCheck = comp.HealthCheck.LastCheck.Format("15:04:05")
			}
			
			t.AppendRow(table.Row{
				comp.Name,
				string(comp.Type),
				hostPort,
				statusIcon + comp.Status,
				healthIcon + comp.HealthCheck.Status,
				comp.PID,
				memory,
				uptime,
				lastCheck,
			})
		} else {
			t.AppendRow(table.Row{
				comp.Name,
				string(comp.Type),
				hostPort,
				statusIcon + comp.Status,
				healthIcon + comp.HealthCheck.Status,
			})
		}
	}
	
	fmt.Println(t.Render())
	
	// Show health summary
	if verbose {
		showHealthSummary(clusterStatus)
	}
	
	return nil
}

func showHealthSummary(clusterStatus *status.ClusterStatus) {
	fmt.Println()
	color.Green("📊 Health Summary")
	
	healthy := 0
	unhealthy := 0
	warning := 0
	
	for _, comp := range clusterStatus.Components {
		switch comp.HealthCheck.Status {
		case "healthy":
			healthy++
		case "unhealthy":
			unhealthy++
		case "warning":
			warning++
		}
	}
	
	fmt.Printf("✅ Healthy: %d\n", healthy)
	if warning > 0 {
		fmt.Printf("⚠️  Warning: %d\n", warning)
	}
	if unhealthy > 0 {
		fmt.Printf("❌ Unhealthy: %d\n", unhealthy)
	}
	
	// Show any errors
	for _, comp := range clusterStatus.Components {
		if comp.HealthCheck.Error != "" {
			color.Red("❌ %s: %s", comp.Name, comp.HealthCheck.Error)
		}
	}
}

func getStateIcon(state status.ClusterState) string {
	switch state {
	case status.StateRunning:
		return "✅ "
	case status.StateStopped:
		return "⛔ "
	case status.StateDegraded:
		return "⚠️  "
	case status.StateError:
		return "❌ "
	default:
		return "❓ "
	}
}

func getStatusIcon(status string) string {
	switch status {
	case "running", "healthy":
		return "✅ "
	case "stopped":
		return "⛔ "
	case "unhealthy":
		return "❌ "
	default:
		return "❓ "
	}
}

func getHealthIcon(health string) string {
	switch health {
	case "healthy":
		return "💚 "
	case "unhealthy":
		return "❤️ "
	case "warning":
		return "💛 "
	default:
		return "🤍 "
	}
}
