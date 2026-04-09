package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/preflight"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/spf13/cobra"
)

// ClusterCheckOptions holds flags for `cluster check`.
type ClusterCheckOptions struct {
	ConfigFile   string
	User         string
	SSHPort      int
	IdentityFile string
	Password     string
	JSONOutput   bool
}

func newClusterCheckCmd() *cobra.Command {
	opts := &ClusterCheckOptions{SSHPort: 22}

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run preflight checks against cluster hosts",
		Long: `Run preflight checks against every host referenced in the cluster
specification. Checks include SSH reachability, passwordless sudo,
free disk space in the data directory, required ports being free,
clock skew, stale weed processes, and arch/os match.

The command exits non-zero if any check fails.`,
		Example: `  seaweed-up cluster check -f cluster.yaml -u root --identity ~/.ssh/id_rsa
  seaweed-up cluster check -f cluster.yaml --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterCheck(cmd, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.ConfigFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "SSH user (default: current user)")
	cmd.Flags().IntVar(&opts.SSHPort, "ssh-port", 22, "default SSH port")
	cmd.Flags().StringVarP(&opts.IdentityFile, "identity", "i", "", "SSH identity file")
	cmd.Flags().StringVarP(&opts.Password, "password", "p", "", "SSH password (optional)")
	cmd.Flags().BoolVar(&opts.JSONOutput, "json", false, "output results as JSON")

	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runClusterCheck(cmd *cobra.Command, opts *ClusterCheckOptions) error {
	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}

	user := opts.User
	if user == "" {
		u, err := utils.CurrentUser()
		if err != nil {
			return fmt.Errorf("failed to get current user: %w", err)
		}
		user = u
	}

	identity := opts.IdentityFile
	if identity == "" && opts.Password == "" {
		home, err := utils.UserHome()
		if err == nil {
			identity = filepath.Join(home, ".ssh", "id_rsa")
		}
	}

	factory := preflight.OperatorSSHFactory(user, identity, opts.Password)
	results := preflight.Run(cmd.Context(), clusterSpec, factory)

	if opts.JSONOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		preflight.Pretty(os.Stdout, results)
	}

	if preflight.HasFailure(results) {
		color.Red("preflight: one or more checks failed")
		return fmt.Errorf("preflight check failed")
	}
	color.Green("preflight: all checks passed")
	return nil
}
