package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/observability"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/spf13/cobra"
)

// ObservabilityOptions collects shared flags for observability subcommands
// that talk to cluster hosts over SSH.
type ObservabilityOptions struct {
	ConfigFile   string
	User         string
	SSHPort      int
	IdentityFile string
}

func newClusterPrometheusConfigCmd() *cobra.Command {
	opts := &ObservabilityOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "prometheus-config [cluster-name]",
		Short: "Print a Prometheus scrape_configs snippet for the cluster",
		Long: `Render a Prometheus scrape_configs YAML snippet covering all SeaweedFS
components (masters, volumes, filers) and node_exporter for every host in
the cluster specification.`,
		Example: `  seaweed-up cluster prometheus-config -f production.yaml
  seaweed-up cluster prometheus-config prod-cluster -f production.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterSpec, err := loadClusterSpec(opts.ConfigFile)
			if err != nil {
				return err
			}
			if len(args) > 0 {
				clusterSpec.Name = args[0]
			}
			fmt.Fprint(cmd.OutOrStdout(), observability.RenderPromConfig(clusterSpec))
			return nil
		},
	}
	cmd.Flags().StringVarP(&opts.ConfigFile, "file", "f", "", "cluster configuration file (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newClusterDashboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Manage SeaweedFS Grafana dashboards",
	}
	cmd.AddCommand(newClusterDashboardInstallCmd())
	return cmd
}

func newClusterDashboardInstallCmd() *cobra.Command {
	var (
		grafanaURL   string
		grafanaToken string
		clusterName  string
		timeout      time.Duration
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the bundled SeaweedFS Grafana dashboard",
		Long: `Upload the bundled SeaweedFS Grafana dashboard to a Grafana instance
via the /api/dashboards/db endpoint.`,
		Example: `  seaweed-up cluster dashboard install \
    --grafana-url=https://grafana.example.com \
    --grafana-token=$GRAFANA_TOKEN \
    --cluster-name=prod`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if grafanaURL == "" {
				return fmt.Errorf("--grafana-url is required")
			}
			if grafanaToken == "" {
				if v := os.Getenv("GRAFANA_TOKEN"); v != "" {
					grafanaToken = v
				} else {
					return fmt.Errorf("--grafana-token is required (or set GRAFANA_TOKEN)")
				}
			}
			client := observability.NewGrafanaClient(grafanaURL, grafanaToken)
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := client.InstallDashboard(ctx, clusterName); err != nil {
				return err
			}
			color.Green("Installed SeaweedFS Grafana dashboard at %s", grafanaURL)
			return nil
		},
	}
	cmd.Flags().StringVar(&grafanaURL, "grafana-url", "", "Grafana base URL (required)")
	cmd.Flags().StringVar(&grafanaToken, "grafana-token", "", "Grafana API token (or env GRAFANA_TOKEN)")
	cmd.Flags().StringVar(&clusterName, "cluster-name", "", "optional cluster name suffix to add to the dashboard title")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "HTTP timeout for Grafana API calls")
	return cmd
}

func newClusterNodeExporterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node-exporter",
		Short: "Manage node_exporter on cluster hosts",
	}
	cmd.AddCommand(newClusterNodeExporterInstallCmd())
	return cmd
}

func newClusterNodeExporterInstallCmd() *cobra.Command {
	opts := &ObservabilityOptions{SSHPort: 22}
	cmd := &cobra.Command{
		Use:   "install [cluster-name]",
		Short: "Install node_exporter on every host in the cluster",
		Long: fmt.Sprintf(`Install the pinned node_exporter release (v%s) on every unique
host referenced by the cluster specification and expose it on :%d as a
systemd service.`, observability.NodeExporterVersion, observability.NodeExporterPort),
		Example: `  seaweed-up cluster node-exporter install -f production.yaml
  seaweed-up cluster node-exporter install prod-cluster -f production.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			clusterSpec, err := loadClusterSpec(opts.ConfigFile)
			if err != nil {
				return err
			}
			if len(args) > 0 {
				clusterSpec.Name = args[0]
			}
			if opts.User == "" {
				u, err := utils.CurrentUser()
				if err != nil {
					return fmt.Errorf("resolve current user: %w", err)
				}
				opts.User = u
			}
			return runNodeExporterInstall(cmd, clusterSpec, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.ConfigFile, "file", "f", "", "cluster configuration file (required)")
	cmd.Flags().StringVarP(&opts.User, "user", "u", "", "SSH user (default: current user)")
	cmd.Flags().IntVarP(&opts.SSHPort, "port", "p", 22, "SSH port")
	cmd.Flags().StringVarP(&opts.IdentityFile, "identity", "i", "", "SSH identity file")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// hostPortsForNodeExporter collects every (host, ssh_port) pair referenced by
// the cluster specification, deduplicated.
func hostPortsForNodeExporter(s *spec.Specification) []hostEntry {
	seen := map[string]struct{}{}
	var out []hostEntry
	add := func(ip string, port int) {
		if ip == "" {
			return
		}
		if port == 0 {
			port = 22
		}
		key := fmt.Sprintf("%s:%d", ip, port)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, hostEntry{IP: ip, SSHPort: port})
	}
	for _, m := range s.MasterServers {
		add(m.Ip, m.PortSsh)
	}
	for _, v := range s.VolumeServers {
		add(v.Ip, v.PortSsh)
	}
	for _, f := range s.FilerServers {
		add(f.Ip, f.PortSsh)
	}
	return out
}

type hostEntry struct {
	IP      string
	SSHPort int
}

func runNodeExporterInstall(cmd *cobra.Command, s *spec.Specification, opts *ObservabilityOptions) error {
	hosts := hostPortsForNodeExporter(s)
	if len(hosts) == 0 {
		return fmt.Errorf("no hosts found in cluster specification")
	}
	out := cmd.OutOrStdout()
	for _, h := range hosts {
		fmt.Fprintf(out, "Installing node_exporter on %s...\n", h.IP)
		addr := fmt.Sprintf("%s:%d", h.IP, h.SSHPort)
		err := operator.ExecuteRemote(addr, opts.User, opts.IdentityFile, "", func(op operator.CommandOperator) error {
			return observability.InstallNodeExporter(op)
		})
		if err != nil {
			return fmt.Errorf("install node_exporter on %s: %w", h.IP, err)
		}
	}
	color.Green("node_exporter installed on %d host(s)", len(hosts))
	return nil
}
