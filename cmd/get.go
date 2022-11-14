package cmd

import (
	"context"
	"fmt"
	"github.com/muesli/coral"
	"github.com/pkg/errors"
	"github.com/seaweedfs/seaweed-up/pkg/config"
)

func GetCommand() *coral.Command {

	var version string
	var arch string
	var os string
	var destination string

	product := "weed"

	var command = &coral.Command{
		Use:          "get",
		Short:        fmt.Sprintf("Download %s on your local machine", product),
		Long:         fmt.Sprintf("Download %s on your local machine", product),
		SilenceUsage: true,
	}

	title := "weed"

	command.Flags().StringVarP(&version, "version", "v", "", fmt.Sprintf("Version of %s to install", title))
	command.Flags().StringVar(&arch, "arch", "amd64", "Target architecture")
	command.Flags().StringVar(&os, "os", "linux", "Target OS")
	command.Flags().StringVarP(&destination, "dest", "d", expandPath("~/bin"), "Target directory for the downloaded archive or binary")

	command.RunE = func(command *coral.Command, args []string) error {

		if len(version) == 0 {
			latest, err := config.GitHubLatestRelease(context.Background(), "0", "seaweedfs", "seaweedfs")

			if err != nil {
				return errors.Wrapf(err, "unable to get latest version number, define a version manually with the --version flag")
			}

			version = latest.Version
		}

		_, err := config.DownloadRelease(context.Background(), os, arch, false, false, destination+"/weed", version)

		if err != nil {
			return errors.Wrapf(err, "unable to download %s distribution", title)
		}

		return nil
	}

	return command
}
