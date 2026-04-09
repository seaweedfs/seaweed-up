package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/scale"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/config"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
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
	ConfigFile   string
	RemoveNodes  []string
	Identity     string
	SkipConfirm  bool
	DrainTimeout time.Duration
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

	if len(opts.RemoveNodes) == 0 {
		return fmt.Errorf("--remove-node is required")
	}
	if opts.ConfigFile == "" {
		return fmt.Errorf("--file/-f cluster configuration file is required")
	}

	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	if clusterName != "" {
		clusterSpec.Name = clusterName
	}
	if len(clusterSpec.MasterServers) == 0 {
		return fmt.Errorf("cluster has no master servers defined")
	}

	// Resolve each --remove-node (host or host:port) to a volume server spec.
	targets, err := resolveRemoveNodes(clusterSpec, opts.RemoveNodes)
	if err != nil {
		return err
	}

	// Pick a healthy master as the drain coordinator. Iterate through all
	// configured masters and use the first that responds to a quick health
	// check, so scale-in still works when masters[0] is down.
	scheme := "http://"
	if clusterSpec.GlobalOptions.TLSEnabled {
		scheme = "https://"
	}
	var master *spec.MasterServerSpec
	var masterPort int
	var masterAddr string
	for _, candidate := range clusterSpec.MasterServers {
		port := nonZero(candidate.Port, 9333)
		addr := fmt.Sprintf("%s%s:%d", scheme, candidate.Ip, port)
		if healthyMaster(addr) {
			master = candidate
			masterPort = port
			masterAddr = addr
			break
		}
		color.Yellow("master %s did not respond to health check; trying next", addr)
	}
	if master == nil {
		return fmt.Errorf("no responsive master found among %d configured master(s)", len(clusterSpec.MasterServers))
	}

	fmt.Printf("Removing %d volume server(s) via master %s:\n", len(targets), masterAddr)
	for _, t := range targets {
		fmt.Printf("  - %s:%d (index %d)\n", t.spec.Ip, nonZero(t.spec.Port, 8080), t.index)
	}

	if !opts.SkipConfirm {
		if !utils.PromptForConfirmation("Proceed with scale-in and data drain?") {
			color.Yellow("⚠️  Scale-in cancelled by user")
			return nil
		}
	}

	sshUser, sshIdentity, sudoPass, err := currentSSHCreds(opts.Identity)
	if err != nil {
		return err
	}

	for _, t := range targets {
		nodeAddr := fmt.Sprintf("%s:%d", t.spec.Ip, nonZero(t.spec.Port, 8080))
		color.Cyan("🫧  Draining volumes from %s ...", nodeAddr)

		drainTimeout := opts.DrainTimeout
		if drainTimeout <= 0 {
			drainTimeout = 30 * time.Minute
		}
		drainErr := scale.Drain(masterAddr, nodeAddr, drainTimeout)
		if drainErr != nil {
			color.Yellow("HTTP drain failed (%v); falling back to weed shell over SSH", drainErr)
			if fbErr := drainViaWeedShell(master.Ip, master.PortSsh, masterPort, sshUser, sshIdentity, sudoPass, nodeAddr); fbErr != nil {
				return fmt.Errorf("drain %s: %w", nodeAddr, fbErr)
			}
		}
		color.Green("✅ Drain complete for %s", nodeAddr)

		if err := removeVolumeNode(t.spec, t.index, sshUser, sshIdentity, sudoPass, clusterSpec); err != nil {
			return fmt.Errorf("remove node %s: %w", nodeAddr, err)
		}
		color.Green("✅ Removed seaweed-volume unit from %s", t.spec.Ip)
	}

	color.Green("✅ Scale-in complete for cluster %s", clusterSpec.Name)
	return nil
}

type volumeTarget struct {
	spec  *spec.VolumeServerSpec
	index int
}

// resolveRemoveNodes matches each user-supplied entry against the cluster's
// volume_servers by ip or ip:port.
func resolveRemoveNodes(clusterSpec *spec.Specification, removeNodes []string) ([]volumeTarget, error) {
	var out []volumeTarget
	for _, raw := range removeNodes {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		host := raw
		port := 0
		if i := strings.LastIndex(raw, ":"); i > 0 {
			if p, err := strconv.Atoi(raw[i+1:]); err == nil {
				host = raw[:i]
				port = p
			}
		}
		var matched *volumeTarget
		for idx, vs := range clusterSpec.VolumeServers {
			if vs.Ip != host {
				continue
			}
			if port != 0 && nonZero(vs.Port, 8080) != port {
				continue
			}
			matched = &volumeTarget{spec: vs, index: idx}
			break
		}
		if matched == nil {
			return nil, fmt.Errorf("node %q not found in cluster volume_servers", raw)
		}
		out = append(out, *matched)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid --remove-node entries")
	}
	return out, nil
}

func nonZero(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// currentSSHCreds returns the current user and identity file used
// by the deploy path so scale-in can reuse the same connection model.
// If identityOverride is non-empty, it is used instead of the default
// ~/.ssh/id_rsa path.
func currentSSHCreds(identityOverride string) (user, identity, sudoPass string, err error) {
	user, err = utils.CurrentUser()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get current user: %w", err)
	}
	if identityOverride != "" {
		identity = identityOverride
	} else {
		home, err := utils.UserHome()
		if err != nil {
			return "", "", "", fmt.Errorf("failed to determine home directory: %w", err)
		}
		identity = filepath.Join(home, ".ssh", "id_rsa")
	}
	if user != "root" {
		sudoPass = utils.PromptForPassword("Input sudo password: ")
	}
	return user, identity, sudoPass, nil
}

// drainViaWeedShell runs `weed shell ... volume.evacuate -node=<target>`
// on a master host via SSH, as a fallback when HTTP drain is unavailable.
func drainViaWeedShell(masterIp string, masterSshPort, masterPort int, user, identity, sudoPass, nodeAddr string) error {
	if masterSshPort == 0 {
		masterSshPort = 22
	}
	if masterPort == 0 {
		masterPort = 9333
	}
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", masterIp, masterSshPort), user, identity, sudoPass, func(op operator.CommandOperator) error {
		cmd := fmt.Sprintf("echo 'volume.evacuate -node=%s' | weed shell -master=%s:%d", nodeAddr, masterIp, masterPort)
		return op.Execute(cmd)
	})
}

// removeVolumeNode stops the systemd unit for the volume server, disables
// it, and removes the data directory and unit file. Mirrors the install.sh
// layout used by manager_deploy.go.
func removeVolumeNode(vs *spec.VolumeServerSpec, index int, user, identity, sudoPass string, clusterSpec *spec.Specification) error {
	host := fmt.Sprintf("%s:%d", vs.Ip, nonZero(vs.PortSsh, 22))
	dataDir := clusterSpec.GlobalOptions.DataDir
	if dataDir == "" {
		dataDir = "/opt/seaweed"
	}
	confDir := clusterSpec.GlobalOptions.ConfigDir
	if confDir == "" {
		confDir = "/etc/seaweed"
	}
	instance := fmt.Sprintf("volume%d", index)
	unit := fmt.Sprintf("seaweed_%s.service", instance)

	// Guard against catastrophic rm: never allow "/" or empty base dirs.
	if !safeRemoveDir(dataDir) {
		return fmt.Errorf("refusing to remove instance under unsafe data_dir %q", dataDir)
	}
	if !safeRemoveDir(confDir) {
		return fmt.Errorf("refusing to remove options file under unsafe config_dir %q", confDir)
	}

	return operator.ExecuteRemote(host, user, identity, sudoPass, func(op operator.CommandOperator) error {
		prefix := ""
		if sudoPass != "" {
			prefix = fmt.Sprintf("echo %s | sudo -S ", shellSingleQuote(sudoPass))
		} else if user != "root" {
			prefix = "sudo "
		}
		instanceDataPath := strings.TrimRight(dataDir, "/") + "/" + instance
		instanceOptionsPath := strings.TrimRight(confDir, "/") + "/" + instance + ".options"
		cmds := []string{
			fmt.Sprintf("%ssystemctl stop %s || true", prefix, unit),
			fmt.Sprintf("%ssystemctl disable %s || true", prefix, unit),
			fmt.Sprintf("%srm -f /etc/systemd/system/%s", prefix, unit),
			fmt.Sprintf("%ssystemctl daemon-reload || true", prefix),
			fmt.Sprintf("%srm -rf %s", prefix, shellSingleQuote(instanceDataPath)),
			fmt.Sprintf("%srm -f %s", prefix, shellSingleQuote(instanceOptionsPath)),
		}
		for _, c := range cmds {
			if err := op.Execute(c); err != nil {
				return fmt.Errorf("exec %q: %w", c, err)
			}
		}
		return nil
	})
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

// shellSingleQuote safely wraps s for use as a single-quoted shell argument.
// Any embedded single quote is escaped as '\'' (close-quote, literal quote,
// reopen-quote). Duplicated from pkg/cluster/manager/manager_lifecycle.go to
// avoid introducing an import cycle between cmd and manager.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// safeRemoveDir rejects obviously dangerous base directories so a misconfigured
// data_dir/config_dir cannot turn scale-in into `rm -rf /...`.
func safeRemoveDir(dir string) bool {
	d := strings.TrimSpace(dir)
	if d == "" {
		return false
	}
	if d == "/" {
		return false
	}
	// Require an absolute path with at least one non-empty segment.
	if !strings.HasPrefix(d, "/") {
		return false
	}
	trimmed := strings.Trim(d, "/")
	return trimmed != ""
}

// healthyMaster performs a fast best-effort GET against a master's
// /cluster/status endpoint. It returns true only if the endpoint responds
// with a 2xx within a short timeout. This is used by scale-in to pick a
// responsive master out of the configured list.
func healthyMaster(masterURL string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(masterURL, "/")+"/cluster/status", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
