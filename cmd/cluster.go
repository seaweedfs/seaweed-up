package cmd

import (
	"github.com/spf13/cobra"
)

func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage SeaweedFS clusters",
		Long: `Cluster management commands for SeaweedFS deployments.

This command group provides comprehensive cluster lifecycle management including:
- Deploy new clusters from configuration files
- Monitor cluster status and health
- Perform rolling upgrades and scaling operations
- Manage cluster configurations and settings`,
		
		Example: `  # Deploy a new cluster
  seaweed-up cluster deploy -f production.yaml
  
  # Check cluster status
  seaweed-up cluster status prod-cluster
  
  # Upgrade cluster
  seaweed-up cluster upgrade prod-cluster --version=3.75
  
  # Scale out cluster
  seaweed-up cluster scale out prod-cluster --add-volume=2`,
	}
	
	// Add cluster subcommands
	cmd.AddCommand(newClusterDeployCmd())
	cmd.AddCommand(newClusterStatusCmd())
	cmd.AddCommand(newClusterUpgradeCmd())
	cmd.AddCommand(newClusterScaleCmd())
	cmd.AddCommand(newClusterDestroyCmd())
	cmd.AddCommand(newClusterListCmd())
	
	return cmd
}

func newClusterDeployCmd() *cobra.Command {
	var (
		configFile      string
		user            string
		sshPort         int
		identityFile    string
		version         string
		component       string
		mountDisks      bool
		forceRestart    bool
		proxyUrl        string
		skipConfirm     bool
	)
	
	cmd := &cobra.Command{
		Use:   "deploy [cluster-name]",
		Short: "Deploy a new SeaweedFS cluster",
		Long: `Deploy a new SeaweedFS cluster from a configuration file.

This command reads a YAML configuration file and deploys the specified
SeaweedFS components across the target hosts using SSH.`,
		
		Example: `  # Deploy from config file
  seaweed-up cluster deploy -f cluster.yaml
  
  # Deploy specific version
  seaweed-up cluster deploy -f cluster.yaml --version=3.75
  
  # Deploy only master components
  seaweed-up cluster deploy -f cluster.yaml --component=master`,
		
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterDeploy(cmd, args, &ClusterDeployOptions{
				ConfigFile:   configFile,
				User:         user,
				SSHPort:      sshPort,
				IdentityFile: identityFile,
				Version:      version,
				Component:    component,
				MountDisks:   mountDisks,
				ForceRestart: forceRestart,
				ProxyUrl:     proxyUrl,
				SkipConfirm:  skipConfirm,
			})
		},
	}
	
	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&user, "user", "u", "", "SSH user (default: current user)")
	cmd.Flags().IntVarP(&sshPort, "port", "p", 22, "SSH port")
	cmd.Flags().StringVarP(&identityFile, "identity", "i", "", "SSH identity file")
	cmd.Flags().StringVar(&version, "version", "", "SeaweedFS version to deploy")
	cmd.Flags().StringVarP(&component, "component", "c", "", "specific component to deploy [master|volume|filer|envoy]")
	cmd.Flags().BoolVar(&mountDisks, "mount-disks", true, "auto mount disks on volume servers")
	cmd.Flags().BoolVar(&forceRestart, "restart", false, "force restart services")
	cmd.Flags().StringVarP(&proxyUrl, "proxy", "x", "", "proxy for downloads (http://proxy:8080)")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip confirmation prompts")
	
	cmd.MarkFlagRequired("file")
	
	return cmd
}

func newClusterStatusCmd() *cobra.Command {
	var (
		jsonOutput bool
		verbose    bool
		timeout    string
		refresh    int
	)
	
	cmd := &cobra.Command{
		Use:   "status [cluster-name]",
		Short: "Show cluster status and health information",
		Long: `Display comprehensive status information for SeaweedFS clusters.

Shows real-time information about cluster components including:
- Process status and health checks
- Resource usage (CPU, memory, disk)
- Network connectivity and performance
- Component versions and configurations`,
		
		Example: `  # Show status for all clusters
  seaweed-up cluster status
  
  # Show status for specific cluster
  seaweed-up cluster status prod-cluster
  
  # Show detailed status in JSON format
  seaweed-up cluster status prod-cluster --json --verbose
  
  # Auto-refresh status every 5 seconds
  seaweed-up cluster status prod-cluster --refresh=5`,
		
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterStatus(args, &ClusterStatusOptions{
				JSONOutput: jsonOutput,
				Verbose:    verbose,
				Timeout:    timeout,
				Refresh:    refresh,
			})
		},
	}
	
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show detailed information")
	cmd.Flags().StringVar(&timeout, "timeout", "30s", "timeout for status collection")
	cmd.Flags().IntVar(&refresh, "refresh", 0, "auto-refresh interval in seconds")
	
	return cmd
}

func newClusterUpgradeCmd() *cobra.Command {
	var (
		version     string
		configFile  string
		skipConfirm bool
		dryRun      bool
	)
	
	cmd := &cobra.Command{
		Use:   "upgrade <cluster-name>",
		Short: "Upgrade cluster to a new SeaweedFS version",
		Long: `Perform rolling upgrade of SeaweedFS cluster components.

This command safely upgrades cluster components with minimal downtime:
- Downloads new binaries automatically
- Performs health checks before and after upgrade
- Supports rollback on failure
- Maintains data availability during upgrade`,
		
		Example: `  # Upgrade to specific version
  seaweed-up cluster upgrade prod-cluster --version=3.75
  
  # Upgrade to latest version
  seaweed-up cluster upgrade prod-cluster --version=latest
  
  # Dry run to see what would be upgraded
  seaweed-up cluster upgrade prod-cluster --version=3.75 --dry-run`,
		
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterUpgrade(args[0], &ClusterUpgradeOptions{
				Version:     version,
				ConfigFile:  configFile,
				SkipConfirm: skipConfirm,
				DryRun:      dryRun,
			})
		},
	}
	
	cmd.Flags().StringVar(&version, "version", "", "target version to upgrade to (required)")
	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip confirmation prompts")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be done without making changes")
	
	cmd.MarkFlagRequired("version")
	
	return cmd
}

func newClusterScaleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scale",
		Short: "Scale cluster components",
		Long: `Scale SeaweedFS cluster by adding or removing components.

Supports both scale-out (adding nodes) and scale-in (removing nodes)
operations with automatic data rebalancing and health verification.`,
	}
	
	cmd.AddCommand(newClusterScaleOutCmd())
	cmd.AddCommand(newClusterScaleInCmd())
	
	return cmd
}

func newClusterScaleOutCmd() *cobra.Command {
	var (
		configFile   string
		addVolume    int
		addFiler     int
		skipConfirm  bool
	)
	
	cmd := &cobra.Command{
		Use:   "out <cluster-name>",
		Short: "Scale out cluster by adding components",
		Long: `Add new components to an existing SeaweedFS cluster.

This command adds new volume servers, filer servers, or other components
to increase cluster capacity and performance.`,
		
		Example: `  # Add 2 volume servers
  seaweed-up cluster scale out prod-cluster --add-volume=2
  
  # Add 1 filer server
  seaweed-up cluster scale out prod-cluster --add-filer=1`,
		
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterScaleOut(args[0], &ClusterScaleOutOptions{
				ConfigFile:  configFile,
				AddVolume:   addVolume,
				AddFiler:    addFiler,
				SkipConfirm: skipConfirm,
			})
		},
	}
	
	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file")
	cmd.Flags().IntVar(&addVolume, "add-volume", 0, "number of volume servers to add")
	cmd.Flags().IntVar(&addFiler, "add-filer", 0, "number of filer servers to add")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip confirmation prompts")
	
	return cmd
}

func newClusterScaleInCmd() *cobra.Command {
	var (
		configFile   string
		removeNodes  []string
		skipConfirm  bool
	)
	
	cmd := &cobra.Command{
		Use:   "in <cluster-name>",
		Short: "Scale in cluster by removing components",
		Long: `Remove components from an existing SeaweedFS cluster.

This command safely removes components after ensuring data is
properly migrated and cluster health is maintained.`,
		
		Example: `  # Remove specific nodes
  seaweed-up cluster scale in prod-cluster --remove-node=node1,node2`,
		
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterScaleIn(args[0], &ClusterScaleInOptions{
				ConfigFile:  configFile,
				RemoveNodes: removeNodes,
				SkipConfirm: skipConfirm,
			})
		},
	}
	
	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file")
	cmd.Flags().StringSliceVar(&removeNodes, "remove-node", nil, "nodes to remove (comma-separated)")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip confirmation prompts")
	
	return cmd
}

func newClusterDestroyCmd() *cobra.Command {
	var (
		configFile    string
		skipConfirm   bool
		removeData    bool
	)
	
	cmd := &cobra.Command{
		Use:   "destroy <cluster-name>",
		Short: "Destroy a SeaweedFS cluster",
		Long: `Completely destroy a SeaweedFS cluster and optionally remove data.

WARNING: This operation is irreversible. All cluster components will be
stopped and removed. Use --remove-data to also delete all stored data.`,
		
		Example: `  # Destroy cluster but keep data
  seaweed-up cluster destroy prod-cluster
  
  # Destroy cluster and remove all data
  seaweed-up cluster destroy prod-cluster --remove-data`,
		
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterDestroy(args[0], &ClusterDestroyOptions{
				ConfigFile:  configFile,
				SkipConfirm: skipConfirm,
				RemoveData:  removeData,
			})
		},
	}
	
	cmd.Flags().StringVarP(&configFile, "file", "f", "", "cluster configuration file")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "skip confirmation prompts")
	cmd.Flags().BoolVar(&removeData, "remove-data", false, "remove all cluster data (WARNING: irreversible)")
	
	return cmd
}

func newClusterListCmd() *cobra.Command {
	var (
		jsonOutput bool
		verbose    bool
	)
	
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all managed clusters",
		Long: `Display a list of all SeaweedFS clusters managed by seaweed-up.

Shows cluster names, status, versions, and basic configuration information.`,
		
		Example: `  # List all clusters
  seaweed-up cluster list
  
  # List clusters with detailed information
  seaweed-up cluster list --verbose
  
  # Output in JSON format
  seaweed-up cluster list --json`,
		
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterList(&ClusterListOptions{
				JSONOutput: jsonOutput,
				Verbose:    verbose,
			})
		},
	}
	
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show detailed information")
	
	return cmd
}
