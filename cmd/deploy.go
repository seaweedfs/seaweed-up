package cmd

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/muesli/coral"
	"github.com/pkg/errors"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/config"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"gopkg.in/yaml.v3"
	"os"
	"path"
)

func DeployCommand() *coral.Command {

	m := manager.NewManager()
	m.IdentityFile = path.Join(utils.UserHome(), ".ssh", "id_rsa")

	var cmd = &coral.Command{
		Use:          "deploy",
		Short:        "deploy a configuration file",
		Long:         "deploy a configuration file",
		SilenceUsage: true,
	}
	var fileName string
	cmd.Flags().StringVarP(&fileName, "file", "f", "", "configuration file")
	cmd.Flags().StringVarP(&m.User, "user", "u", utils.CurrentUser(), "The user name to login via SSH. The user must has root (or sudo) privilege.")
	cmd.Flags().IntVarP(&m.SshPort, "port", "p", 22, "The port to SSH.")
	cmd.Flags().StringVarP(&m.IdentityFile, "identity_file", "i", m.IdentityFile, "The path of the SSH identity file. If specified, public key authentication will be used.")
	cmd.Flags().StringVarP(&m.Version, "version", "v", "", "The SeaweedFS version")
	cmd.Flags().StringVarP(&m.ComponentToDeploy, "component", "c", "", "[master|volume|filer|envoy] only install one component")
	cmd.Flags().BoolVarP(&m.PrepareVolumeDisks, "mountDisks", "", true, "auto mount disks on volume server if unmounted")
	cmd.Flags().BoolVarP(&m.ForceRestart, "restart", "", false, "force to restart the service")
	cmd.Flags().StringVarP(&m.ProxyUrl, "proxy", "x", "", "proxy for curl in format PROTO://PROXY (example: http://someproxy.com:8080/)")

	cmd.RunE = func(command *coral.Command, args []string) error {

		if m.Version == "" {
			latest, err := config.GitHubLatestRelease(context.Background(), "0", "seaweedfs", "seaweedfs")
			if err != nil {
				return errors.Wrapf(err, "unable to get latest version number, define a version manually with the --version flag")
			}
			m.Version = latest.Version
		}

		fmt.Println(fileName)
		spec := &spec.Specification{}
		data, readErr := os.ReadFile(fileName)
		if readErr != nil {
			return fmt.Errorf("read %s: %v", fileName, readErr)
		}
		if unmarshalErr := yaml.Unmarshal(data, spec); unmarshalErr != nil {
			return fmt.Errorf("unmarshal %s: %v", fileName, unmarshalErr)
		}

		return m.DeployCluster(spec)
	}

	return cmd
}
