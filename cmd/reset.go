package cmd

import (
	_ "embed"
	"fmt"
	"github.com/muesli/coral"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"gopkg.in/yaml.v3"
	"os"
	"path"
)

func CleanCommand() *coral.Command {

	m := manager.NewManager()
	m.IdentityFile = path.Join(utils.UserHome(), ".ssh", "id_rsa")

	var cmd = &coral.Command{
		Use:          "clean",
		Short:        "clean a cluster storage to empty",
		Long:         "clean a cluster storage to empty",
		SilenceUsage: true,
	}
	var fileName string
	cmd.Flags().StringVarP(&fileName, "file", "f", "", "configuration file")
	cmd.Flags().StringVarP(&m.User, "user", "u", utils.CurrentUser(), "The user name to login via SSH. The user must has root (or sudo) privilege.")
	cmd.Flags().IntVarP(&m.SshPort, "port", "p", 22, "The port to SSH.")
	cmd.Flags().StringVarP(&m.IdentityFile, "identity_file", "i", m.IdentityFile, "The path of the SSH identity file. If specified, public key authentication will be used.")
	cmd.Flags().StringVarP(&m.Version, "version", "v", "", "The SeaweedFS version")
	cmd.Flags().StringVarP(&m.ComponentToDeploy, "component", "c", "", "[master|volume|filer] only clean one component")

	cmd.RunE = func(command *coral.Command, args []string) error {

		fmt.Println(fileName)
		spec := &spec.Specification{}
		data, readErr := os.ReadFile(fileName)
		if readErr != nil {
			return fmt.Errorf("read %s: %v", fileName, readErr)
		}
		if unmarshalErr := yaml.Unmarshal(data, spec); unmarshalErr != nil {
			return fmt.Errorf("unmarshal %s: %v", fileName, unmarshalErr)
		}

		return m.CleanCluster(spec)
	}

	return cmd
}
