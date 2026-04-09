package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	tlsbootstrap "github.com/seaweedfs/seaweed-up/pkg/cluster/tls"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/spf13/cobra"
)

// ClusterCertOptions holds options shared by `cluster cert` subcommands.
type ClusterCertOptions struct {
	ConfigFile   string
	User         string
	SSHPort      int
	IdentityFile string
	SkipUpload   bool
}

func newClusterCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Manage TLS certificates for a SeaweedFS cluster",
		Long: `Generate, rotate and distribute TLS certificates for a SeaweedFS cluster.

The 'init' subcommand creates a fresh self-signed CA and a per-host
certificate for every master/volume/filer in the cluster spec, stores them
under ~/.seaweed-up/clusters/<name>/certs/ and uploads them to the target
hosts along with a generated security.toml.

The 'rotate' subcommand reuses the existing CA and re-issues all per-host
certificates.`,
	}

	cmd.AddCommand(newClusterCertInitCmd())
	cmd.AddCommand(newClusterCertRotateCmd())
	return cmd
}

func certCommonFlags(cmd *cobra.Command, opts *ClusterCertOptions) {
	cmd.Flags().StringVarP(&opts.ConfigFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "SSH user (default: current user)")
	cmd.Flags().IntVarP(&opts.SSHPort, "port", "p", 22, "SSH port")
	cmd.Flags().StringVarP(&opts.IdentityFile, "identity", "i", "", "SSH identity file")
	cmd.Flags().BoolVar(&opts.SkipUpload, "local-only", false, "only generate certs locally, do not SSH-upload")
	_ = cmd.MarkFlagRequired("file")
}

func newClusterCertInitCmd() *cobra.Command {
	opts := &ClusterCertOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "init <cluster-name>",
		Short: "Generate a new CA and per-host certificates and distribute them",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterCertInit(args[0], opts)
		},
	}
	certCommonFlags(cmd, opts)
	return cmd
}

func newClusterCertRotateCmd() *cobra.Command {
	opts := &ClusterCertOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "rotate <cluster-name>",
		Short: "Re-issue per-host certificates using the existing CA",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterCertRotate(args[0], opts)
		},
	}
	certCommonFlags(cmd, opts)
	return cmd
}

func runClusterCertInit(name string, opts *ClusterCertOptions) error {
	return runCertFlow(name, opts, true)
}

func runClusterCertRotate(name string, opts *ClusterCertOptions) error {
	return runCertFlow(name, opts, false)
}

func runCertFlow(name string, opts *ClusterCertOptions, init bool) error {
	clusterSpec, err := loadClusterSpec(opts.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}
	if clusterSpec.Name == "" {
		clusterSpec.Name = name
	}

	var bs *tlsbootstrap.Bootstrap
	if init {
		color.Green("Generating new CA and per-host certificates for %s", name)
		bs, err = tlsbootstrap.InitCluster(name, clusterSpec)
	} else {
		color.Green("Rotating per-host certificates for %s", name)
		bs, err = tlsbootstrap.RotateCluster(name, clusterSpec)
	}
	if err != nil {
		return err
	}
	color.Cyan("Certificates written to %s", bs.Dir)

	if opts.SkipUpload {
		return nil
	}

	user := opts.User
	if user == "" {
		u, err := utils.CurrentUser()
		if err != nil {
			return fmt.Errorf("determine ssh user: %w", err)
		}
		user = u
	}
	identity := opts.IdentityFile
	if identity == "" {
		home, err := utils.UserHome()
		if err != nil {
			return fmt.Errorf("determine home dir: %w", err)
		}
		identity = filepath.Join(home, ".ssh", "id_rsa")
	}

	if err := uploadCertsToHosts(clusterSpec, bs, user, identity, opts.SSHPort); err != nil {
		return err
	}
	color.Green("Certificate distribution complete")
	return nil
}

// uploadCertsToHosts connects to each host in the spec and uploads the
// appropriate cert/key bundle plus security.toml.
func uploadCertsToHosts(clusterSpec *spec.Specification, bs *tlsbootstrap.Bootstrap, user, identity string, sshPort int) error {
	type target struct {
		component string
		ip        string
		port      int
	}
	var targets []target
	for _, m := range clusterSpec.MasterServers {
		targets = append(targets, target{"master", m.Ip, nvlPort(m.PortSsh, sshPort)})
	}
	for _, v := range clusterSpec.VolumeServers {
		targets = append(targets, target{"volume", v.Ip, nvlPort(v.PortSsh, sshPort)})
	}
	for _, f := range clusterSpec.FilerServers {
		targets = append(targets, target{"filer", f.Ip, nvlPort(f.PortSsh, sshPort)})
	}

	for _, t := range targets {
		addr := fmt.Sprintf("%s:%d", t.ip, t.port)
		color.Cyan("Uploading %s certs to %s", t.component, addr)
		err := operator.ExecuteRemote(addr, user, identity, "", func(op operator.CommandOperator) error {
			return tlsbootstrap.DistributeHost(op, bs, t.component, t.ip)
		})
		if err != nil {
			return fmt.Errorf("distribute to %s: %w", addr, err)
		}
	}
	return nil
}

func nvlPort(v, def int) int {
	if v > 0 {
		return v
	}
	if def > 0 {
		return def
	}
	return 22
}
