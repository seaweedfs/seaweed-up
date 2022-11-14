package cmd

import (
	_ "embed"
	"fmt"
	"github.com/muesli/coral"
)

func ScaffoldCommand() *coral.Command {

	var command = &coral.Command{
		Use:          "scaffold",
		Short:        "scaffold an example configuration file",
		Long:         "scaffold an example configuration file",
		SilenceUsage: true,
	}

	command.RunE = func(command *coral.Command, args []string) error {

		fmt.Println(clusterYaml)

		return nil
	}

	return command
}

//go:embed cluster.yaml
var clusterYaml string
