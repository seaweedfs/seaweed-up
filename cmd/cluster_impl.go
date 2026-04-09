package cmd

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
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
	Version           string
	ConfigFile        string
	User              string
	SSHPort           int
	IdentityFile      string
	SkipConfirm       bool
	DryRun            bool
	RollbackOnFailure bool
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

	if opts.Version == "" {
		return fmt.Errorf("--version is required")
	}

	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	if clusterName != "" {
		clusterSpec.Name = clusterName
	}

	mgr := manager.NewManager()
	mgr.SshPort = opts.SSHPort
	if mgr.SshPort == 0 {
		mgr.SshPort = 22
	}

	if opts.User == "" {
		currentUser, err := utils.CurrentUser()
		if err != nil {
			return fmt.Errorf("failed to get current user for SSH: %w", err)
		}
		mgr.User = currentUser
	} else {
		mgr.User = opts.User
	}

	if opts.IdentityFile == "" {
		home, err := utils.UserHome()
		if err != nil {
			return fmt.Errorf("failed to determine home directory for SSH identity file: %w", err)
		}
		mgr.IdentityFile = filepath.Join(home, ".ssh", "id_rsa")
	} else {
		mgr.IdentityFile = opts.IdentityFile
	}

	if opts.DryRun {
		color.Yellow("🔍 Dry run mode - no changes will be made")
	} else if !opts.SkipConfirm {
		color.Yellow("📋 Upgrade Summary:")
		fmt.Printf("  Cluster: %s\n", clusterSpec.Name)
		fmt.Printf("  Target Version: %s\n", opts.Version)
		fmt.Printf("  Masters: %d\n", len(clusterSpec.MasterServers))
		fmt.Printf("  Volumes: %d\n", len(clusterSpec.VolumeServers))
		fmt.Printf("  Filers:  %d\n", len(clusterSpec.FilerServers))
		fmt.Printf("  Rollback on failure: %v\n", opts.RollbackOnFailure)
		if !utils.PromptForConfirmation("Proceed with rolling upgrade?") {
			color.Yellow("⚠️  Upgrade cancelled by user")
			return nil
		}
	}

	// Probe the currently-running cluster version so that rollback has a target
	// to restore to. If probing fails we proceed with previousVersion="" and
	// rollback will be disabled for this run.
	if current, err := probeCurrentClusterVersion(clusterSpec); err != nil {
		color.Yellow("⚠️  Could not determine current cluster version (%v); rollback will be disabled.", err)
		mgr.Version = ""
	} else {
		color.Cyan("ℹ️  Current cluster version detected: %s", current)
		mgr.Version = current
	}

	upgradeOpts := manager.UpgradeOptions{
		RollbackOnFailure: opts.RollbackOnFailure,
		DryRun:            opts.DryRun,
	}

	if err := mgr.UpgradeCluster(clusterSpec, opts.Version, upgradeOpts); err != nil {
		color.Red("❌ Upgrade failed: %v", err)
		return err
	}

	if opts.DryRun {
		color.Green("✅ Dry-run complete.")
	} else {
		color.Green("✅ Cluster upgraded to %s", opts.Version)
	}
	return nil
}

// versionRegex captures semver-ish version tokens like "3.64" or "v3.64.1".
var versionRegex = regexp.MustCompile(`v?\d+\.\d+(?:\.\d+)?`)

// probeCurrentClusterVersion asks one of the master hosts for its running
// version. It tries /cluster/status first and falls back to /dir/status,
// looking in both the JSON body and the Server response header.
//
// TODO: plumb the cluster CA bundle through so we can set
// InsecureSkipVerify=false. For now we intentionally skip verification so
// upgrades work on self-signed clusters out of the box.
func probeCurrentClusterVersion(clusterSpec *spec.Specification) (string, error) {
	if len(clusterSpec.MasterServers) == 0 {
		return "", fmt.Errorf("no master servers in spec")
	}
	scheme := "http"
	if clusterSpec.GlobalOptions.TLSEnabled {
		scheme = "https"
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // see TODO above
			},
		},
	}
	var lastErr error
	for _, ms := range clusterSpec.MasterServers {
		for _, path := range []string{"/cluster/status", "/dir/status"} {
			url := fmt.Sprintf("%s://%s:%d%s", scheme, ms.Ip, ms.Port, path)
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				lastErr = err
				continue
			}
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				lastErr = fmt.Errorf("status %d from %s", resp.StatusCode, url)
				_ = resp.Body.Close()
				continue
			}
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			serverHeader := resp.Header.Get("Server")
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("read body from %s: %w", url, readErr)
				continue
			}
			// Try common JSON fields.
			var payload map[string]interface{}
			if jsonErr := json.Unmarshal(body, &payload); jsonErr == nil {
				for _, key := range []string{"Version", "version"} {
					if v, ok := payload[key].(string); ok && v != "" {
						if m := versionRegex.FindString(v); m != "" {
							return m, nil
						}
						return v, nil
					}
				}
			}
			// Fall back to the Server response header.
			if serverHeader != "" {
				if m := versionRegex.FindString(serverHeader); m != "" {
					return m, nil
				}
			}
			// Last-ditch: regex scan the raw body.
			if m := versionRegex.FindString(string(body)); m != "" {
				return m, nil
			}
			lastErr = fmt.Errorf("no version found in response from %s", url)
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no master responded")
	}
	return "", lastErr
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

	// Validate the specification
	if err := clusterSpec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid cluster specification: %w", err)
	}

	return clusterSpec, nil
}
