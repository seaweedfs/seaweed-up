package cmd

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
	Version               string
	ConfigFile            string
	User                  string
	SSHPort               int
	IdentityFile          string
	SkipConfirm           bool
	DryRun                bool
	RollbackOnFailure     bool
	InsecureSkipTLSVerify bool
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
	color.Green("Deploying SeaweedFS cluster...")
	
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
		color.Yellow("Deployment Summary:")
		fmt.Printf("  Cluster: %s\n", clusterSpec.Name)
		fmt.Printf("  Version: %s\n", mgr.Version)
		fmt.Printf("  Masters: %d\n", len(clusterSpec.MasterServers))
		fmt.Printf("  Volumes: %d\n", len(clusterSpec.VolumeServers))
		fmt.Printf("  Filers:  %d\n", len(clusterSpec.FilerServers))
		
		if !utils.PromptForConfirmation("Proceed with deployment?") {
			color.Yellow("WARN: Deployment cancelled by user")
			return nil
		}
	}
	
	// Deploy cluster
	if err := mgr.DeployCluster(clusterSpec); err != nil {
		color.Red("Error: Deployment failed: %v", err)
		return err
	}

	// Persist cluster topology + metadata so later commands can
	// resolve by name.
	if clusterSpec.Name != "" {
		store, storeErr := state.NewStore("")
		if storeErr != nil {
			color.Yellow("WARN: Unable to open state store: %v", storeErr)
		} else {
			meta := state.Meta{
				Name:       clusterSpec.Name,
				Version:    mgr.Version,
				DeployedAt: time.Now().UTC(),
				Hosts:      state.HostsFromSpec(clusterSpec),
			}
			if err := store.Save(clusterSpec.Name, clusterSpec, meta); err != nil {
				color.Yellow("WARN: Failed to persist cluster state: %v", err)
			}
		}
	} else {
		color.Yellow("WARN: Cluster has no name; skipping state persistence")
	}

	color.Green("Cluster deployed successfully!")
	color.Cyan("Next steps:")
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
		color.Cyan("Refreshing every %d seconds (Press Ctrl+C to stop)", opts.Refresh)

		for {
			select {
			case <-sigChan:
				// Graceful shutdown on interrupt
				fmt.Println()
				color.Yellow("Refresh stopped by user")
				return nil
			case <-ticker.C:
				clearScreen()
				if err := displayClusterStatus(args, opts); err != nil {
					return err
				}
				color.Cyan("Refreshing every %d seconds (Press Ctrl+C to stop)", opts.Refresh)
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
	color.Green("Cluster Status")
	
	if len(args) == 0 {
		color.Yellow("All Clusters:")
		// Show all clusters
		fmt.Println("No clusters found. Deploy a cluster first with 'seaweed-up cluster deploy'")
	} else {
		clusterName := args[0]
		color.Yellow("Cluster: %s", clusterName)
		fmt.Println("Status collection not yet implemented")
	}
	
	return nil
}

func runClusterUpgrade(clusterName string, opts *ClusterUpgradeOptions) error {
	color.Green("Upgrading cluster: %s to version %s", clusterName, opts.Version)

	if opts.Version == "" {
		return fmt.Errorf("--version is required")
	}

	clusterSpec, err := resolveSpec([]string{clusterName}, opts.ConfigFile)
	if err != nil {
		return err
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
		color.Yellow("Dry run mode - no changes will be made")
	} else if !opts.SkipConfirm {
		color.Yellow("Upgrade Summary:")
		fmt.Printf("  Cluster: %s\n", clusterSpec.Name)
		fmt.Printf("  Target Version: %s\n", opts.Version)
		fmt.Printf("  Masters: %d\n", len(clusterSpec.MasterServers))
		fmt.Printf("  Volumes: %d\n", len(clusterSpec.VolumeServers))
		fmt.Printf("  Filers:  %d\n", len(clusterSpec.FilerServers))
		fmt.Printf("  Rollback on failure: %v\n", opts.RollbackOnFailure)
		if !utils.PromptForConfirmation("Proceed with rolling upgrade?") {
			color.Yellow("WARN: Upgrade cancelled by user")
			return nil
		}
	}

	// Probe the currently-running cluster version so that rollback has a target
	// to restore to. If probing fails we proceed with previousVersion="" and
	// rollback will be disabled for this run.
	if current, err := probeCurrentClusterVersion(clusterSpec, opts.InsecureSkipTLSVerify); err != nil {
		color.Yellow("WARN: Could not determine current cluster version (%v); rollback will be disabled.", err)
		mgr.Version = ""
	} else if current == "" {
		color.Yellow("WARN: Current cluster version could not be parsed from master response; rollback will be disabled.")
		mgr.Version = ""
	} else {
		color.Cyan("Current cluster version detected: %s", current)
		mgr.Version = current
	}

	upgradeOpts := manager.UpgradeOptions{
		RollbackOnFailure:     opts.RollbackOnFailure,
		DryRun:                opts.DryRun,
		InsecureSkipTLSVerify: opts.InsecureSkipTLSVerify,
	}

	if err := mgr.UpgradeCluster(clusterSpec, opts.Version, upgradeOpts); err != nil {
		color.Red("Error: Upgrade failed: %v", err)
		return err
	}

	if opts.DryRun {
		color.Green("Dry-run complete.")
	} else {
		color.Green("Cluster upgraded to %s", opts.Version)
	}
	return nil
}

// seaweedVersionRegexes are ordered-by-preference patterns used to extract a
// SeaweedFS version from a master's /dir/status or /cluster/status response.
//
// SeaweedFS versions look like "30GB 3.85 b2f34c..." (disk-unit, version,
// commit) or "seaweedfs 3.85". We deliberately avoid a bare `\d+\.\d+` match
// because the master's response can legitimately contain IP addresses (e.g.
// "172.28.0.10") which would otherwise be mis-parsed as versions — that caused
// a rolling upgrade rollback to try to reinstall "172.28.0" and fail.
var seaweedVersionRegexes = []*regexp.Regexp{
	// "30GB 3.85 abcd1234" — the canonical /dir/status Version field.
	regexp.MustCompile(`\b\d+\s*[KMGT]B\s+(\d+\.\d+(?:\.\d+)?)\b`),
	// "seaweedfs 3.85" or "weed 3.85".
	regexp.MustCompile(`(?i)\b(?:seaweedfs|weed)\s+v?(\d+\.\d+(?:\.\d+)?)\b`),
	// "seaweedfs/3.85" (e.g. Server header).
	regexp.MustCompile(`(?i)\b(?:seaweedfs|weed)/v?(\d+\.\d+(?:\.\d+)?)\b`),
}

// extractSeaweedVersion returns a version token from s, or "" if nothing
// plausible is found. Matches must come from the Seaweed-specific patterns
// above; a raw IP address like "172.28.0.10" will never match.
func extractSeaweedVersion(s string) string {
	if s == "" {
		return ""
	}
	for _, re := range seaweedVersionRegexes {
		if m := re.FindStringSubmatch(s); len(m) >= 2 && m[1] != "" {
			return m[1]
		}
	}
	return ""
}

// dirStatusPayload is the minimal subset of SeaweedFS's /dir/status response
// we care about for version probing.
type dirStatusPayload struct {
	Version string `json:"Version"`
}

// probeCurrentClusterVersion asks the master hosts for their running version.
// It tries /dir/status first and falls back to /cluster/status, preferring a
// typed JSON Version field and then falling back to the Server response header.
//
// The parser is strict: it only accepts tokens that match a Seaweed-specific
// version pattern. An IP-like substring ("172.28.0.10") will never be returned.
// If a master responds 2xx but no version could be extracted, probing
// continues with the remaining masters; "" + nil is returned only when all
// masters responded successfully without a recognizable version.
//
// When the cluster is TLS-enabled, the default http.Client uses the system
// cert pool. Pass insecureSkipTLSVerify=true to disable certificate
// verification (for self-signed dev clusters).
//
// TODO: use cluster CA once tls bootstrap PR lands.
func probeCurrentClusterVersion(clusterSpec *spec.Specification, insecureSkipTLSVerify bool) (string, error) {
	if len(clusterSpec.MasterServers) == 0 {
		return "", fmt.Errorf("no master servers in spec")
	}
	scheme := "http"
	if clusterSpec.GlobalOptions.TLSEnabled {
		scheme = "https"
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: newUpgradeHTTPTransport(insecureSkipTLSVerify),
	}
	var lastErr error
	sawHealthyMasterWithoutVersion := false
	for _, ms := range clusterSpec.MasterServers {
		for _, path := range []string{"/dir/status", "/cluster/status"} {
			addr := net.JoinHostPort(ms.Ip, strconv.Itoa(ms.Port))
			url := fmt.Sprintf("%s://%s%s", scheme, addr, path)
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
			// Prefer the typed JSON Version field.
			var typed dirStatusPayload
			if jsonErr := json.Unmarshal(body, &typed); jsonErr == nil && typed.Version != "" {
				if v := extractSeaweedVersion(typed.Version); v != "" {
					return v, nil
				}
			}
			// Generic map lookup covers responses that use lowercase "version".
			var payload map[string]interface{}
			if jsonErr := json.Unmarshal(body, &payload); jsonErr == nil {
				for _, key := range []string{"Version", "version"} {
					if raw, ok := payload[key].(string); ok && raw != "" {
						if v := extractSeaweedVersion(raw); v != "" {
							return v, nil
						}
					}
				}
			}
			// Fall back to the Server response header.
			if v := extractSeaweedVersion(serverHeader); v != "" {
				return v, nil
			}
			// Nothing matched on this endpoint. Remember that this master
			// responded 2xx but didn't yield a recognizable version, and
			// keep probing other masters/endpoints.
			sawHealthyMasterWithoutVersion = true
		}
	}
	if sawHealthyMasterWithoutVersion {
		// All reachable masters responded but none exposed a parseable
		// version. Return empty + nil so the caller proceeds with rollback
		// disabled rather than treating this as a hard error.
		return "", nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no master responded")
	}
	return "", lastErr
}

// newUpgradeHTTPTransport builds an http.Transport for upgrade probes.
//
// Default behavior uses the system cert pool (tls.Config{}). When
// insecureSkipTLSVerify is true, certificate verification is disabled.
//
// TODO: use cluster CA once tls bootstrap PR lands.
func newUpgradeHTTPTransport(insecureSkipTLSVerify bool) *http.Transport {
	tlsConfig := &tls.Config{}
	if insecureSkipTLSVerify {
		tlsConfig.InsecureSkipVerify = true //nolint:gosec // explicitly requested via --insecure-skip-tls-verify
	}
	return &http.Transport{TLSClientConfig: tlsConfig}
}

func runClusterScaleOut(clusterName string, opts *ClusterScaleOutOptions) error {
	color.Green("Scaling out cluster: %s", clusterName)
	
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
	color.Green("Scaling in cluster: %s", clusterName)
	
	if len(opts.RemoveNodes) > 0 {
		fmt.Printf("Removing nodes: %v\n", opts.RemoveNodes)
	}
	
	// TODO: Implement scale in logic
	fmt.Println("Scale in functionality not yet implemented")
	
	return nil
}

func runClusterDestroy(clusterName string, opts *ClusterDestroyOptions) error {
	color.Red("WARNING: This will destroy cluster '%s'", clusterName)
	
	if opts.RemoveData {
		color.Red("WARN: ALL DATA WILL BE PERMANENTLY DELETED!")
	}
	
	if !opts.SkipConfirm {
		confirmation := utils.PromptForInput("Type the cluster name to confirm destruction: ")
		
		if confirmation != clusterName {
			color.Yellow("WARN: Destruction cancelled - cluster name didn't match")
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

	color.Green("Managed Clusters")
	if len(entries) == 0 {
		fmt.Println("No clusters found. Deploy a cluster first with 'seaweed-up cluster deploy'")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tVERSION\tHOSTS\tMASTERS\tVOLUMES\tFILERS\tDEPLOYED")
	for _, e := range entries {
		deployed := e.Meta.DeployedAt.Local().Format("2006-01-02 15:04:05")
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%s\n",
			e.Meta.Name,
			e.Meta.Version,
			len(e.Meta.Hosts),
			len(e.Spec.MasterServers),
			len(e.Spec.VolumeServers),
			len(e.Spec.FilerServers),
			deployed,
		)
		if opts.Verbose && len(e.Meta.Hosts) > 0 {
			_, _ = fmt.Fprintf(tw, "  hosts: %s\t\t\t\t\t\t\n", strings.Join(e.Meta.Hosts, ", "))
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
