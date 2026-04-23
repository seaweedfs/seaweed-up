package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"github.com/seaweedfs/seaweed-up/pkg/cluster/health"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/preflight"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/scale"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/state"
	sutls "github.com/seaweedfs/seaweed-up/pkg/cluster/tls"
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
	EnvoyVersion string
	Component    string
	MountDisks   bool
	HostPrep     bool
	ForceRestart bool
	ProxyUrl     string
	SkipConfirm  bool
	TLS          bool
	Check        bool
	Concurrency  int
	// Enterprise selects the public SeaweedFS enterprise release repo
	// (github.com/seaweedfs/artifactory) as the binary source instead of
	// the OSS repo. No authentication is required — both repos are
	// public, though $GITHUB_TOKEN / $GH_TOKEN is still honored on the
	// release metadata lookup to dodge the 60 req/hr anonymous rate
	// limit on shared CI runners.
	Enterprise bool
}

type ClusterStatusOptions struct {
	ConfigFile string
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
	// Enterprise pulls target binaries from the public SeaweedFS
	// enterprise release repo (github.com/seaweedfs/artifactory).
	Enterprise bool
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
	User         string
	SSHPort      int
	Identity     string
	SkipConfirm  bool
	DrainTimeout time.Duration
}

type ClusterDestroyOptions struct {
	ConfigFile   string
	User         string
	SSHPort      int
	IdentityFile string
	SkipConfirm  bool
	RemoveData   bool
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
	mgr.EnvoyVersion = opts.EnvoyVersion
	mgr.ComponentToDeploy = opts.Component
	mgr.PrepareVolumeDisks = opts.MountDisks
	mgr.HostPrep = opts.HostPrep
	mgr.ForceRestart = opts.ForceRestart
	mgr.ProxyUrl = opts.ProxyUrl
	mgr.Concurrency = opts.Concurrency
	mgr.Enterprise = opts.Enterprise

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
	
	// Run preflight checks first if requested
	if opts.Check {
		color.Cyan("Running preflight checks...")
		// TODO: plumb sudo password from deploy options once ClusterDeployOptions
		// exposes a Password/SudoPass field so preflight sudo checks work on
		// hosts that require a password.
		factory := preflight.OperatorSSHFactory(mgr.User, mgr.IdentityFile, "")
		results := preflight.RunWithOptions(cmd.Context(), clusterSpec, factory, preflight.Options{
			DefaultSSHPort: opts.SSHPort,
		})
		preflight.Pretty(os.Stdout, results)
		if preflight.HasFailure(results) {
			return fmt.Errorf("preflight checks failed; aborting deploy")
		}
		color.Green("Preflight checks passed")
	}

	// Get latest version if not specified. Enterprise deploys look up the
	// version from the public artifactory repo instead of the OSS repo.
	if mgr.Version == "" {
		owner, repo := mgr.ReleaseOwnerRepo()
		latest, err := config.GitHubLatestRelease(cmd.Context(), "0", owner, repo)
		if err != nil {
			return fmt.Errorf("unable to get latest version from %s/%s: %w", owner, repo, err)
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
		fmt.Printf("  Admins:  %d\n", len(clusterSpec.AdminServers))
		fmt.Printf("  Workers: %d\n", len(clusterSpec.WorkerServers))

		if !utils.PromptForConfirmation("Proceed with deployment?") {
			color.Yellow("Deployment cancelled by user")
			return nil
		}
	}

	// If --tls requested, bootstrap the CA and distribute certs BEFORE
	// starting services so that the rendered security.toml is in place
	// when seaweed processes come up. We also stamp TLSEnabled=true on
	// the in-memory spec so that it is persisted by state.Save below
	// and picked up by later commands (status, upgrade, etc.) even if
	// the source YAML omitted `global.enable_tls`.
	if opts.TLS {
		clusterSpec.GlobalOptions.TLSEnabled = true
		color.Cyan("Bootstrapping TLS for cluster %q", clusterSpec.Name)
		certOpts := &ClusterCertOptions{
			ConfigFile:   opts.ConfigFile,
			User:         mgr.User,
			SSHPort:      opts.SSHPort,
			IdentityFile: mgr.IdentityFile,
		}
		if err := runClusterCertInit(clusterSpec.Name, certOpts, false); err != nil {
			color.Red("Error: TLS bootstrap failed: %v", err)
			return err
		}
	}

	// Deploy cluster
	if err := mgr.DeployCluster(clusterSpec); err != nil {
		color.Red("FAIL: Deployment failed: %v", err)
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

func runClusterStatus(cmd *cobra.Command, args []string, opts *ClusterStatusOptions) error {
	clusterSpec, err := resolveStatusSpec(args, opts)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

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
		if _, derr := displayClusterStatus(ctx, clusterSpec, opts); derr != nil {
			return derr
		}
		color.Cyan("Refreshing every %d seconds (Press Ctrl+C to stop)", opts.Refresh)

		for {
			select {
			case <-sigChan:
				fmt.Println()
				color.Yellow("Refresh stopped by user")
				return nil
			case <-ticker.C:
				clearScreen()
				if _, derr := displayClusterStatus(ctx, clusterSpec, opts); derr != nil {
					return derr
				}
				color.Cyan("Refreshing every %d seconds (Press Ctrl+C to stop)", opts.Refresh)
			}
		}
	}

	ch, err := displayClusterStatus(ctx, clusterSpec, opts)
	if err != nil {
		return err
	}
	if !ch.AllHealthy() {
		unhealthy := ch.UnhealthyCount()
		return fmt.Errorf("%d component(s) unhealthy", unhealthy)
	}
	return nil
}

// resolveStatusSpec loads the topology either from -f or by looking up a
// positional cluster-name file next to the binary's working directory.
func resolveStatusSpec(args []string, opts *ClusterStatusOptions) (*spec.Specification, error) {
	configFile := opts.ConfigFile
	if configFile == "" && len(args) > 0 {
		// Positional-name fallback: try <name>.yaml / <name>.yml
		candidates := []string{args[0] + ".yaml", args[0] + ".yml", args[0]}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				configFile = c
				break
			}
		}
	}
	if configFile == "" {
		return nil, fmt.Errorf("cluster configuration file is required (use -f)")
	}
	s, err := loadClusterSpec(configFile)
	if err != nil {
		return nil, err
	}
	if s.Name == "" && len(args) > 0 {
		s.Name = args[0]
	}
	return s, nil
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

// displayClusterStatus probes the cluster and prints the result, returning
// the aggregated health so callers can decide on process exit status.
func displayClusterStatus(ctx context.Context, clusterSpec *spec.Specification, opts *ClusterStatusOptions) (*health.ClusterHealth, error) {
	// Parse the user-configured timeout (Cobra flag, default "30s"). Fall
	// back to a 5s safety default only if parsing fails or the value is
	// empty / non-positive.
	timeout := 5 * time.Second
	if d, err := time.ParseDuration(opts.Timeout); err == nil && d > 0 {
		timeout = d
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()

	// Derive the probe scheme and trust roots from the cluster spec so
	// TLS-enabled clusters are probed over HTTPS with the cluster CA.
	certDir, _ := sutls.LocalClusterDir(clusterSpec.Name)
	prober := health.NewProberForSpec(timeout, clusterSpec, certDir)
	ch := prober.Probe(probeCtx, clusterSpec)

	if opts.JSONOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(ch); err != nil {
			return ch, err
		}
		return ch, nil
	}

	if err := renderStatusTable(clusterSpec, ch, opts.Verbose); err != nil {
		return ch, err
	}
	return ch, nil
}

func renderStatusTable(s *spec.Specification, ch *health.ClusterHealth, verbose bool) error {
	name := s.Name
	if name == "" {
		name = "(unnamed)"
	}
	color.Green("Cluster Status: %s", name)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "COMPONENT\tADDRESS\tHEALTHY\tVERSION\tDETAIL"); err != nil {
		return fmt.Errorf("write status header: %w", err)
	}
	writeRow := func(r health.ProbeResult) error {
		healthy := "NO"
		if r.Healthy {
			healthy = "OK"
		}
		// In verbose mode show the error (if any) followed by the raw
		// response body, so users can diagnose failures without losing
		// context. In non-verbose mode we only surface the error string.
		detail := r.Err
		if verbose && r.Raw != nil {
			if b, err := json.Marshal(r.Raw); err == nil {
				if detail != "" {
					detail = detail + " | " + string(b)
				} else {
					detail = string(b)
				}
			}
		}
		if detail == "" {
			detail = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Kind, r.Address, healthy, orDash(r.Version), detail); err != nil {
			return fmt.Errorf("write status row: %w", err)
		}
		return nil
	}
	for _, r := range ch.Masters {
		if err := writeRow(r); err != nil {
			return err
		}
	}
	for _, r := range ch.Volumes {
		if err := writeRow(r); err != nil {
			return err
		}
	}
	for _, r := range ch.Filers {
		if err := writeRow(r); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush status table: %w", err)
	}

	if !ch.AllHealthy() {
		color.Red("Cluster is UNHEALTHY")
	} else {
		color.Green("Cluster is healthy")
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
	mgr.Enterprise = opts.Enterprise

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
		fmt.Printf("  Admins:  %d\n", len(clusterSpec.AdminServers))
		fmt.Printf("  Workers: %d\n", len(clusterSpec.WorkerServers))
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
		Transport: newUpgradeHTTPTransport(insecureSkipTLSVerify, clusterSpec.Name),
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
// If the cluster has a CA at ~/.seaweed-up/clusters/<name>/certs/ca.crt
// (produced by `cluster cert init` / `cluster deploy --tls`), that CA
// is added as a trust root. If no CA is found, the transport falls back
// to the system cert pool. InsecureSkipTLSVerify is honored only when
// explicitly requested.
func newUpgradeHTTPTransport(insecureSkipTLSVerify bool, clusterName string) *http.Transport {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if insecureSkipTLSVerify {
		tlsConfig.InsecureSkipVerify = true //nolint:gosec // explicitly requested via --insecure-skip-tls-verify
	} else if clusterName != "" {
		if dir, err := sutls.LocalClusterDir(clusterName); err == nil {
			if pemBytes, rerr := os.ReadFile(filepath.Join(dir, "ca.crt")); rerr == nil && len(pemBytes) > 0 {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(pemBytes) {
					tlsConfig.RootCAs = pool
				}
			}
		}
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
	// HTTP client aware of the cluster CA when TLS is enabled. Used for the
	// master health probe and for the post-drain verification poll, both of
	// which hit the master over HTTPS on TLS clusters.
	masterHTTPClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: newUpgradeHTTPTransport(false, clusterSpec.Name),
	}
	var master *spec.MasterServerSpec
	var masterPort int
	var masterAddr string
	for _, candidate := range clusterSpec.MasterServers {
		port := nonZero(candidate.Port, 9333)
		addr := fmt.Sprintf("%s%s:%d", scheme, candidate.Ip, port)
		if healthyMasterWithClient(masterHTTPClient, addr) {
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
			color.Yellow("WARN: Scale-in cancelled by user")
			return nil
		}
	}

	sshUser, sshIdentity, sudoPass, err := currentSSHCreds(opts.User, opts.Identity)
	if err != nil {
		return err
	}

	for _, t := range targets {
		nodeAddr := fmt.Sprintf("%s:%d", t.spec.Ip, nonZero(t.spec.Port, 8080))
		color.Cyan("Draining volumes from %s ...", nodeAddr)

		drainTimeout := opts.DrainTimeout
		if drainTimeout <= 0 {
			drainTimeout = 30 * time.Minute
		}
		masterSshPort := nonZero(master.PortSsh, opts.SSHPort)
		if err := drainViaWeedShell(master.Ip, masterSshPort, masterPort, sshUser, sshIdentity, sudoPass, nodeAddr); err != nil {
			return fmt.Errorf("drain %s: %w", nodeAddr, err)
		}
		if err := scale.WaitForDrainWithClient(masterHTTPClient, masterAddr, nodeAddr, drainTimeout); err != nil {
			return fmt.Errorf("drain %s: %w", nodeAddr, err)
		}
		color.Green("Drain complete for %s", nodeAddr)

		if err := removeVolumeNode(t.spec, t.index, sshUser, sshIdentity, sudoPass, opts.SSHPort, clusterSpec); err != nil {
			return fmt.Errorf("remove node %s: %w", nodeAddr, err)
		}
		color.Green("Removed seaweed-volume unit from %s", t.spec.Ip)
	}

	color.Green("Scale-in complete for cluster %s", clusterSpec.Name)
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

// currentSSHCreds returns the user and identity file used by the deploy path
// so scale-in can reuse the same connection model.
//
// If userOverride is non-empty, it is used instead of the current OS user.
// If identityOverride is non-empty, it is used instead of the default
// ~/.ssh/id_rsa path. Relative identity paths are resolved to an absolute
// path against the current working directory so later SSH calls (which may
// run inside goroutines or after a chdir) all read the exact same key file.
func currentSSHCreds(userOverride, identityOverride string) (user, identity, sudoPass string, err error) {
	if userOverride != "" {
		user = userOverride
	} else {
		user, err = utils.CurrentUser()
		if err != nil {
			return "", "", "", fmt.Errorf("failed to get current user: %w", err)
		}
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
	// Resolve identity to absolute path when it is not already absolute and
	// is not a ~-prefixed path (which operator.expandPath will handle).
	if identity != "" && !filepath.IsAbs(identity) && !strings.HasPrefix(identity, "~") {
		if abs, absErr := filepath.Abs(identity); absErr == nil {
			identity = abs
		}
	}
	if user != "root" {
		sudoPass = utils.PromptForPassword("Input sudo password: ")
	}
	return user, identity, sudoPass, nil
}

// drainViaWeedShell runs `weed shell ... volumeServer.evacuate -node=<target> -apply`
// on a master host via SSH. This is the only mechanism SeaweedFS exposes to
// move volumes off a server — there is no master HTTP endpoint for it.
//
// `weed shell` exits 0 even when the command is unknown or evacuation fails,
// so we capture stdout/stderr and inspect it for known failure phrases.
func drainViaWeedShell(masterIp string, masterSshPort, masterPort int, user, identity, sudoPass, nodeAddr string) error {
	if masterSshPort == 0 {
		masterSshPort = 22
	}
	if masterPort == 0 {
		masterPort = 9333
	}
	sshHost := net.JoinHostPort(masterIp, strconv.Itoa(masterSshPort))
	masterAddr := net.JoinHostPort(masterIp, strconv.Itoa(masterPort))
	return operator.ExecuteRemote(sshHost, user, identity, sudoPass, func(op operator.CommandOperator) error {
		// `volumeServer.evacuate -apply` calls confirmIsLocked, so the weed
		// shell session must acquire the admin lock first. Pipe `lock`,
		// then the evacuate command, then `unlock`. Use `printf '%s\n'` so
		// the evacuate line is not subject to printf format interpretation.
		evacuateCmd := fmt.Sprintf("volumeServer.evacuate -node=%s -apply", nodeAddr)
		cmd := fmt.Sprintf("printf '%%s\\n' lock %s unlock | weed shell -master=%s 2>&1",
			shellSingleQuote(evacuateCmd), shellSingleQuote(masterAddr))
		out, err := op.Output(cmd)
		text := string(out)
		if len(text) > 0 {
			fmt.Println(strings.TrimRight(text, "\n"))
		}
		if err != nil {
			return fmt.Errorf("weed shell volumeServer.evacuate: %w", err)
		}
		lower := strings.ToLower(text)
		if strings.Contains(lower, "unknown command") {
			return fmt.Errorf("weed shell rejected volumeServer.evacuate (output: %s)", strings.TrimSpace(text))
		}
		if strings.Contains(lower, `need to run "lock" first`) {
			return fmt.Errorf("weed shell refused evacuate: admin lock not acquired (output: %s)", strings.TrimSpace(text))
		}
		if strings.Contains(lower, "no such") || strings.Contains(lower, "failed to evacuate") {
			return fmt.Errorf("weed shell volumeServer.evacuate reported failure: %s", strings.TrimSpace(text))
		}
		return nil
	})
}

// removeVolumeNode stops the systemd unit for the volume server, disables
// it, and removes the data directory and unit file. Mirrors the install.sh
// layout used by manager_deploy.go.
func removeVolumeNode(vs *spec.VolumeServerSpec, index int, user, identity, sudoPass string, defaultSshPort int, clusterSpec *spec.Specification) error {
	sshPort := nonZero(vs.PortSsh, defaultSshPort)
	sshPort = nonZero(sshPort, 22)
	host := fmt.Sprintf("%s:%d", vs.Ip, sshPort)
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
	color.Red("WARNING: This will destroy cluster '%s'", clusterName)

	if opts.RemoveData {
		color.Red("ALL DATA WILL BE PERMANENTLY DELETED!")
	}

	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	if clusterName != "" {
		clusterSpec.Name = clusterName
	}

	if !opts.SkipConfirm {
		prompt := fmt.Sprintf("Type the cluster name '%s' to confirm destruction: ", clusterSpec.Name)
		confirmation := utils.PromptForInput(prompt)

		if confirmation != clusterSpec.Name {
			color.Yellow("WARN: Destruction cancelled - cluster name didn't match")
			return nil
		}
	}

	mgr, err := newManagerForLifecycle(opts.SSHPort, opts.User, opts.IdentityFile)
	if err != nil {
		return err
	}

	color.Yellow("Destroying cluster components...")
	if err := mgr.DestroyCluster(clusterSpec, opts.RemoveData); err != nil {
		color.Red("FAIL: Destroy failed: %v", err)
		return err
	}

	color.Green("Cluster destroyed successfully")
	if opts.RemoveData {
		color.Green("Data and configuration directories removed")
	}
	return nil
}

// newManagerForLifecycle builds a manager.Manager populated with SSH
// credentials suitable for lifecycle operations (start/stop/restart/destroy).
// Zero/empty values fall back to sensible defaults.
func newManagerForLifecycle(sshPort int, user, identityFile string) (*manager.Manager, error) {
	mgr := manager.NewManager()

	if sshPort == 0 {
		sshPort = 22
	}
	mgr.SshPort = sshPort

	if user == "" {
		currentUser, err := utils.CurrentUser()
		if err != nil {
			return nil, fmt.Errorf("failed to get current user for SSH: %w", err)
		}
		mgr.User = currentUser
	} else {
		mgr.User = user
	}

	if identityFile == "" {
		home, err := utils.UserHome()
		if err != nil {
			return nil, fmt.Errorf("failed to determine home directory for SSH identity file: %w", err)
		}
		mgr.IdentityFile = filepath.Join(home, ".ssh", "id_rsa")
	} else {
		mgr.IdentityFile = identityFile
	}

	return mgr, nil
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
			Admins     int       `json:"admins"`
			Workers    int       `json:"workers"`
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
				Admins:     len(e.Spec.AdminServers),
				Workers:    len(e.Spec.WorkerServers),
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
	_, _ = fmt.Fprintln(tw, "NAME\tVERSION\tHOSTS\tMASTERS\tVOLUMES\tFILERS\tADMINS\tWORKERS\tDEPLOYED")
	for _, e := range entries {
		deployed := e.Meta.DeployedAt.Local().Format("2006-01-02 15:04:05")
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			e.Meta.Name,
			e.Meta.Version,
			len(e.Meta.Hosts),
			len(e.Spec.MasterServers),
			len(e.Spec.VolumeServers),
			len(e.Spec.FilerServers),
			len(e.Spec.AdminServers),
			len(e.Spec.WorkerServers),
			deployed,
		)
		if opts.Verbose && len(e.Meta.Hosts) > 0 {
			_, _ = fmt.Fprintf(tw, "  hosts: %s\t\t\t\t\t\t\t\t\n", strings.Join(e.Meta.Hosts, ", "))
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

// healthyMasterWithClient performs a fast best-effort GET against a master's
// /cluster/status endpoint. It returns true only if the endpoint responds
// with a 2xx within a short timeout. The caller supplies the HTTP client so
// TLS clusters can pass one that trusts the cluster CA.
func healthyMasterWithClient(client *http.Client, masterURL string) bool {
	probe := *client
	probe.Timeout = 3 * time.Second
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(masterURL, "/")+"/cluster/status", nil)
	if err != nil {
		return false
	}
	resp, err := probe.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
