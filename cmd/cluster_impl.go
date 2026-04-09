package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/state"
	"github.com/seaweedfs/seaweed-up/pkg/config"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/spf13/cobra"
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

func runClusterDeploy(cmd *cobra.Command, args []string, opts *ClusterDeployOptions) error {
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
	mgr.SshPort = opts.SSHPort
	mgr.Version = opts.Version
	mgr.ComponentToDeploy = opts.Component
	mgr.PrepareVolumeDisks = opts.MountDisks
	mgr.ForceRestart = opts.ForceRestart
	mgr.ProxyUrl = opts.ProxyUrl
	
	// Default SSH user to current user if not specified
	if opts.User == "" {
		currentUser, err := utils.CurrentUser()
		if err != nil {
			return fmt.Errorf("failed to get current user for SSH: %w", err)
		}
		mgr.User = currentUser
	} else {
		mgr.User = opts.User
	}
	
	// Default identity file to ~/.ssh/id_rsa if not specified
	if opts.IdentityFile == "" {
		home, err := utils.UserHome()
		if err != nil {
			return fmt.Errorf("failed to determine home directory for SSH identity file: %w", err)
		}
		mgr.IdentityFile = filepath.Join(home, ".ssh", "id_rsa")
	} else {
		mgr.IdentityFile = opts.IdentityFile
	}
	
	// Get latest version if not specified
	if mgr.Version == "" {
		latest, err := config.GitHubLatestRelease(cmd.Context(), "0", "seaweedfs", "seaweedfs")
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

	// Persist cluster topology + metadata so later commands can
	// resolve by name.
	if clusterSpec.Name != "" {
		store, storeErr := state.NewStore("")
		if storeErr != nil {
			color.Yellow("⚠️  Unable to open state store: %v", storeErr)
		} else {
			meta := state.Meta{
				Name:       clusterSpec.Name,
				Version:    mgr.Version,
				DeployedAt: time.Now().UTC(),
				Hosts:      state.HostsFromSpec(clusterSpec),
			}
			if err := store.Save(clusterSpec.Name, clusterSpec, meta); err != nil {
				color.Yellow("⚠️  Failed to persist cluster state: %v", err)
			}
		}
	} else {
		color.Yellow("⚠️  Cluster has no name; skipping state persistence")
	}

	color.Green("✅ Cluster deployed successfully!")
	color.Cyan("💡 Next steps:")
	fmt.Println("  - Check cluster status: seaweed-up cluster status", clusterSpec.Name)
	fmt.Println("  - View logs: seaweed-up cluster logs", clusterSpec.Name)
	
	return nil
}

func runClusterStatus(args []string, opts *ClusterStatusOptions) error {
	// Handle auto-refresh mode with proper signal handling
	if opts.Refresh > 0 {
		ticker := time.NewTicker(time.Duration(opts.Refresh) * time.Second)
		defer ticker.Stop()

		// Set up signal handling for graceful shutdown
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		// Show initial status
		clearScreen()
		if err := displayClusterStatus(args, opts); err != nil {
			return err
		}
		color.Cyan("🔄 Refreshing every %d seconds (Press Ctrl+C to stop)", opts.Refresh)

		for {
			select {
			case <-sigChan:
				// Graceful shutdown on interrupt
				fmt.Println()
				color.Yellow("⏹️  Refresh stopped by user")
				return nil
			case <-ticker.C:
				clearScreen()
				if err := displayClusterStatus(args, opts); err != nil {
					return err
				}
				color.Cyan("🔄 Refreshing every %d seconds (Press Ctrl+C to stop)", opts.Refresh)
			}
		}
	}

	return displayClusterStatus(args, opts)
}

// clearScreen clears the terminal screen in a cross-platform way
func clearScreen() {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to clear screen: %v\n", err)
		}
	default:
		// Unix-like systems (Linux, macOS, etc.)
		fmt.Print("\033[H\033[2J")
	}
}

// displayClusterStatus shows the actual cluster status
func displayClusterStatus(args []string, opts *ClusterStatusOptions) error {
	// TODO: Implement actual status collection
	color.Green("📊 Cluster Status")
	
	if len(args) == 0 {
		color.Yellow("📋 All Clusters:")
		// Show all clusters
		fmt.Println("No clusters found. Deploy a cluster first with 'seaweed-up cluster deploy'")
	} else {
		clusterName := args[0]
		color.Yellow("📋 Cluster: %s", clusterName)
		fmt.Println("Status collection not yet implemented")
	}
	
	return nil
}

func runClusterUpgrade(clusterName string, opts *ClusterUpgradeOptions) error {
	color.Green("⬆️  Upgrading cluster: %s to version %s", clusterName, opts.Version)

	if opts.DryRun {
		color.Yellow("🔍 Dry run mode - no changes will be made")
	}

	clusterSpec, err := resolveSpec([]string{clusterName}, opts.ConfigFile)
	if err != nil {
		return err
	}
	fmt.Printf("  Masters: %d\n", len(clusterSpec.MasterServers))
	fmt.Printf("  Volumes: %d\n", len(clusterSpec.VolumeServers))
	fmt.Printf("  Filers:  %d\n", len(clusterSpec.FilerServers))

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
		confirmation := utils.PromptForInput("Type the cluster name to confirm destruction: ")
		
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
	store, err := state.NewStore("")
	if err != nil {
		return fmt.Errorf("open state store: %w", err)
	}
	entries, err := store.List()
	if err != nil {
		return fmt.Errorf("list clusters: %w", err)
	}

	if opts.JSONOutput {
		type jsonEntry struct {
			Name       string    `json:"name"`
			Version    string    `json:"version"`
			DeployedAt time.Time `json:"deployed_at"`
			Hosts      []string  `json:"hosts"`
			Masters    int       `json:"masters"`
			Volumes    int       `json:"volumes"`
			Filers     int       `json:"filers"`
		}
		out := make([]jsonEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, jsonEntry{
				Name:       e.Meta.Name,
				Version:    e.Meta.Version,
				DeployedAt: e.Meta.DeployedAt,
				Hosts:      e.Meta.Hosts,
				Masters:    len(e.Spec.MasterServers),
				Volumes:    len(e.Spec.VolumeServers),
				Filers:     len(e.Spec.FilerServers),
			})
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	color.Green("📋 Managed Clusters")
	if len(entries) == 0 {
		fmt.Println("No clusters found. Deploy a cluster first with 'seaweed-up cluster deploy'")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVERSION\tHOSTS\tMASTERS\tVOLUMES\tFILERS\tDEPLOYED")
	for _, e := range entries {
		deployed := e.Meta.DeployedAt.Local().Format("2006-01-02 15:04:05")
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
			e.Meta.Name,
			e.Meta.Version,
			len(e.Meta.Hosts),
			len(e.Spec.MasterServers),
			len(e.Spec.VolumeServers),
			len(e.Spec.FilerServers),
			deployed,
		)
		if opts.Verbose && len(e.Meta.Hosts) > 0 {
			fmt.Fprintf(tw, "  hosts: %s\t\t\t\t\t\t\n", strings.Join(e.Meta.Hosts, ", "))
		}
	}
	return tw.Flush()
}

// resolveSpec returns the cluster specification to operate on.
// If cfgFile is set, it is loaded and returned. Otherwise, the first
// positional argument is treated as a cluster name and looked up in
// the persistent state store. A clear error is returned if neither
// resolution path succeeds.
func resolveSpec(args []string, cfgFile string) (*spec.Specification, error) {
	if cfgFile != "" {
		return loadClusterSpec(cfgFile)
	}
	if len(args) == 0 || args[0] == "" {
		return nil, fmt.Errorf("cluster name or -f/--file is required")
	}
	name := args[0]
	store, err := state.NewStore("")
	if err != nil {
		return nil, fmt.Errorf("open state store: %w", err)
	}
	if !store.Exists(name) {
		return nil, fmt.Errorf("no persisted cluster named %q; pass -f to specify a configuration file", name)
	}
	sp, _, err := store.Load(name)
	if err != nil {
		return nil, fmt.Errorf("load cluster %q: %w", name, err)
	}
	return sp, nil
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

	// Validate the specification
	if err := clusterSpec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid cluster specification: %w", err)
	}

	return clusterSpec, nil
}
