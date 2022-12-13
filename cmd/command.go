package cmd

import (
	"fmt"
	"github.com/mitchellh/go-homedir"
	"github.com/muesli/coral"
)

type Installer func() *coral.Command

func Execute() error {

	rootCmd := baseCommand("seaweed-up")
	rootCmd.AddCommand(TlsCommands())
	rootCmd.AddCommand(VersionCommand())
	rootCmd.AddCommand(GetCommand())
	rootCmd.AddCommand(ScaffoldCommand())
	rootCmd.AddCommand(DeployCommand())
	rootCmd.AddCommand(ResetCommand())

	return rootCmd.Execute()
}

func baseCommand(name string) *coral.Command {
	return &coral.Command{
		Use: name,
		Run: func(cmd *coral.Command, args []string) {
			cmd.Help()
		},
		SilenceErrors: true,
	}
}

func expandPath(path string) string {
	res, _ := homedir.Expand(path)
	return res
}

func info(message string) {
	fmt.Println("[INFO] " + message)
}
