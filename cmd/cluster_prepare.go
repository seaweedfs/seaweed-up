package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/spf13/cobra"
)

// ClusterPrepareOptions carries configuration for the `cluster prepare` /
// `cluster host-prep` subcommand.
type ClusterPrepareOptions struct {
	ConfigFile   string
	User         string
	SSHPort      int
	IdentityFile string
	SkipConfirm  bool
}

func newClusterPrepareCmd() *cobra.Command {
	opts := &ClusterPrepareOptions{SSHPort: 22}

	cmd := &cobra.Command{
		Use:     "prepare [cluster-name]",
		Aliases: []string{"host-prep"},
		Short:   "Prepare target hosts (ulimits, sysctls, firewall, time sync)",
		Long: `Run host preparation on every host in the cluster specification.

This uploads and executes an idempotent script that:
  - Writes /etc/security/limits.d/seaweed.conf (nofile 1048576)
  - Writes /etc/sysctl.d/99-seaweed.conf (vm.max_map_count, net.core.somaxconn, fs.file-max)
  - Opens SeaweedFS ports on ufw/firewalld/iptables
  - Enables chrony or systemd-timesyncd for time sync`,
		Example: `  seaweed-up cluster prepare -f cluster.yaml
  seaweed-up cluster host-prep -f cluster.yaml --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterPrepare(cmd, args, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.ConfigFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "SSH user (default: current user)")
	cmd.Flags().IntVarP(&opts.SSHPort, "port", "p", 22, "SSH port")
	cmd.Flags().StringVarP(&opts.IdentityFile, "identity", "i", "", "SSH identity file")
	cmd.Flags().BoolVarP(&opts.SkipConfirm, "yes", "y", false, "skip confirmation prompts")

	_ = cmd.MarkFlagRequired("file")

	return cmd
}

func runClusterPrepare(cmd *cobra.Command, args []string, opts *ClusterPrepareOptions) error {
	color.Green("Preparing hosts for SeaweedFS...")

	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	if len(args) > 0 {
		clusterSpec.Name = args[0]
	}

	mgr := manager.NewManager()
	mgr.SshPort = opts.SSHPort
	mgr.HostPrep = true

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

	if !opts.SkipConfirm {
		color.Yellow("Host preparation summary:")
		fmt.Printf("  Cluster: %s\n", clusterSpec.Name)
		fmt.Printf("  Masters: %d\n", len(clusterSpec.MasterServers))
		fmt.Printf("  Volumes: %d\n", len(clusterSpec.VolumeServers))
		fmt.Printf("  Filers:  %d\n", len(clusterSpec.FilerServers))
		fmt.Printf("  Envoys:  %d\n", len(clusterSpec.EnvoyServers))
		if !utils.PromptForConfirmation("Proceed with host preparation?") {
			color.Yellow("Host preparation cancelled by user")
			return nil
		}
	}

	// Apply spec defaults (fills in PortSsh etc.) then run host prep.
	if err := manager.PrepareHostsForSpec(mgr, clusterSpec); err != nil {
		color.Red("Host preparation failed: %v", err)
		return err
	}

	color.Green("Host preparation complete.")
	return nil
}
