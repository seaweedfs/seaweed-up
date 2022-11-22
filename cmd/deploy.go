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

func DeployCommand() *coral.Command {

	m := manager.NewManager("3.33")
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
	cmd.Flags().StringVarP(&m.IdentityFile, "identity_file", "i", m.IdentityFile, "The path of the SSH identity file. If specified, public key authentication will be used.")
	cmd.Flags().BoolVarP(&m.UsePassword, "password", "p", false, "Use password of target hosts. If specified, password authentication will be used.")

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

		return m.Deploy(spec)
	}

	return cmd
}
