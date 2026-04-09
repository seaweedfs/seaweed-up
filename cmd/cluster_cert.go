package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/fatih/color"
	sutls "github.com/seaweedfs/seaweed-up/pkg/cluster/tls"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/spf13/cobra"
)

// ClusterCertOptions is shared by `cluster cert init` and `cluster cert rotate`.
type ClusterCertOptions struct {
	ConfigFile   string
	User         string
	SSHPort      int
	IdentityFile string
}

func newClusterCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Manage TLS certificates for a SeaweedFS cluster",
		Long: `Generate a private CA and per-component certificates for a
SeaweedFS cluster, then distribute them to all hosts referenced by a
topology file and install /etc/seaweed/security.toml.`,
	}
	cmd.AddCommand(newClusterCertInitCmd())
	cmd.AddCommand(newClusterCertRotateCmd())
	return cmd
}

func newClusterCertInitCmd() *cobra.Command {
	opts := &ClusterCertOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "init <cluster-name>",
		Short: "Generate CA, issue per-host certs, and distribute them",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterCertInit(args[0], opts, false)
		},
	}
	addCertFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newClusterCertRotateCmd() *cobra.Command {
	opts := &ClusterCertOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "rotate <cluster-name>",
		Short: "Re-issue per-host certificates from the existing CA",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterCertInit(args[0], opts, true)
		},
	}
	addCertFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func addCertFlags(cmd *cobra.Command, opts *ClusterCertOptions) {
	cmd.Flags().StringVarP(&opts.ConfigFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "SSH user (default: current user)")
	cmd.Flags().IntVarP(&opts.SSHPort, "port", "p", 22, "SSH port")
	cmd.Flags().StringVarP(&opts.IdentityFile, "identity", "i", "", "SSH identity file")
}

func runClusterCertInit(clusterName string, opts *ClusterCertOptions, rotate bool) error {
	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	clusterSpec.Name = clusterName

	user := opts.User
	if user == "" {
		u, err := utils.CurrentUser()
		if err != nil {
			return fmt.Errorf("failed to get current user: %w", err)
		}
		user = u
	}

	identity := opts.IdentityFile
	if identity == "" {
		home, err := utils.UserHome()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		identity = filepath.Join(home, ".ssh", "id_rsa")
	}

	if rotate {
		color.Cyan("Rotating certs for cluster %q", clusterName)
	} else {
		color.Cyan("Bootstrapping TLS for cluster %q", clusterName)
	}

	caPEM, caKeyPEM, err := sutls.LoadOrGenerateCA(clusterName)
	if err != nil {
		return fmt.Errorf("load/generate CA: %w", err)
	}

	hosts := sutls.AllHosts(clusterSpec)
	if len(hosts) == 0 {
		return fmt.Errorf("no hosts found in cluster spec")
	}

	for _, h := range hosts {
		port := h.SSHPort
		if port == 0 {
			port = opts.SSHPort
		}
		color.Yellow("  -> %s (%s)", h.IP, h.Role)
		bundle, err := sutls.BuildHostBundle(caPEM, caKeyPEM, h.IP)
		if err != nil {
			return fmt.Errorf("build bundle for %s: %w", h.IP, err)
		}
		if err := sutls.PersistHostBundle(clusterName, h.IP, bundle); err != nil {
			return fmt.Errorf("persist bundle for %s: %w", h.IP, err)
		}

		address := fmt.Sprintf("%s:%d", h.IP, port)
		err = operator.ExecuteRemote(address, user, identity, "", func(op operator.CommandOperator) error {
			return sutls.UploadBundle(op, h.Role, bundle)
		})
		if err != nil {
			return fmt.Errorf("upload bundle to %s: %w", h.IP, err)
		}
	}

	color.Green("TLS bootstrap complete for cluster %q (%d hosts)", clusterName, len(hosts))
	return nil
}
