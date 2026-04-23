package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/spf13/cobra"
)

// ClusterLifecycleOptions are shared by the start/stop/restart subcommands.
type ClusterLifecycleOptions struct {
	ConfigFile   string
	Component    string
	User         string
	IdentityFile string
	SSHPort      int
	SkipConfirm  bool
}

func newClusterStartCmd() *cobra.Command {
	opts := &ClusterLifecycleOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start SeaweedFS cluster services",
		Long:  "Start all seaweed systemd units across every host in the cluster.",
		Example: `  # Start every component in the cluster
  seaweed-up cluster start -f cluster.yaml

  # Start only volume servers
  seaweed-up cluster start -f cluster.yaml --component=volume`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterLifecycle(manager.LifecycleStart, opts)
		},
	}
	addLifecycleFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newClusterStopCmd() *cobra.Command {
	opts := &ClusterLifecycleOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop SeaweedFS cluster services",
		Long:  "Stop all seaweed systemd units across every host in the cluster.",
		Example: `  # Stop every component
  seaweed-up cluster stop -f cluster.yaml

  # Stop only filer servers
  seaweed-up cluster stop -f cluster.yaml --component=filer`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterLifecycle(manager.LifecycleStop, opts)
		},
	}
	addLifecycleFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newClusterRestartCmd() *cobra.Command {
	opts := &ClusterLifecycleOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart SeaweedFS cluster services",
		Long:  "Restart all seaweed systemd units across every host in the cluster.",
		Example: `  # Restart the whole cluster
  seaweed-up cluster restart -f cluster.yaml

  # Restart only master servers
  seaweed-up cluster restart -f cluster.yaml --component=master`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterLifecycle(manager.LifecycleRestart, opts)
		},
	}
	addLifecycleFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func addLifecycleFlags(cmd *cobra.Command, opts *ClusterLifecycleOptions) {
	cmd.Flags().StringVarP(&opts.ConfigFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&opts.Component, "component", "c", "", "specific component to target [master|volume|filer|envoy|s3|sftp|admin|worker]")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "SSH user (default: current user)")
	cmd.Flags().IntVarP(&opts.SSHPort, "port", "p", 22, "SSH port")
	cmd.Flags().StringVarP(&opts.IdentityFile, "identity", "i", "", "SSH identity file")
	cmd.Flags().BoolVarP(&opts.SkipConfirm, "yes", "y", false, "skip confirmation prompts")
}

func runClusterLifecycle(verb manager.LifecycleVerb, opts *ClusterLifecycleOptions) error {
	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}

	switch opts.Component {
	case "", "master", "volume", "filer", "envoy", "s3", "sftp", "admin", "worker":
	default:
		return fmt.Errorf("invalid --component value %q (want master|volume|filer|envoy|s3|sftp|admin|worker)", opts.Component)
	}

	if !opts.SkipConfirm {
		scope := "all components"
		if opts.Component != "" {
			scope = opts.Component + " servers"
		}
		question := fmt.Sprintf("About to %s %s on cluster '%s'. Proceed?", verb, scope, clusterSpec.Name)
		if !utils.PromptForConfirmation(question) {
			color.Yellow("WARN: %s cancelled by user", verb)
			return nil
		}
	}

	mgr, err := newManagerForLifecycle(opts.SSHPort, opts.User, opts.IdentityFile)
	if err != nil {
		return err
	}

	color.Cyan("Running systemctl %s across cluster...", verb)
	switch verb {
	case manager.LifecycleStart:
		err = mgr.StartCluster(clusterSpec, opts.Component)
	case manager.LifecycleStop:
		err = mgr.StopCluster(clusterSpec, opts.Component)
	case manager.LifecycleRestart:
		err = mgr.RestartCluster(clusterSpec, opts.Component)
	default:
		return fmt.Errorf("unknown lifecycle verb %q", verb)
	}
	if err != nil {
		color.Red("FAIL: %s failed: %v", verb, err)
		return err
	}
	color.Green("%s complete", verb)
	return nil
}
