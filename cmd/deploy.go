package cmd

import (
	_ "embed"
	"fmt"
	"github.com/muesli/coral"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/manager"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func DeployCommand() *coral.Command {

	var command = &coral.Command{
		Use:          "deploy",
		Short:        "deploy a configuration file",
		Long:         "deploy a configuration file",
		SilenceUsage: true,
	}
	var fileName string
	command.Flags().StringVar(&fileName, "f", "", "configuration file")

	command.RunE = func(command *coral.Command, args []string) error {

		fmt.Println(fileName)

		m := manager.NewManager("3.33")

		spec := &spec.Specification{
			GlobalOptions: spec.GlobalOptions{
				User:       "chris",
				Group:      "",
				SSHPort:    2222, // not used
				TLSEnabled: false,
				DeployDir:  "",
				DataDir:    "",
				LogDir:     "",
				OS:         "",
				Arch:       "",
			},
			MasterServers: []*spec.MasterServerSpec{
				{
					Ip:                 "localhost",
					PortSsh:            2222,
					IpBind:             "",
					Port:               9334,
					PortGrpc:           0,
					VolumeSizeLimitMB:  1000,
					DefaultReplication: "",
					MetricsPort:        0,
					DeployDir:          "",
					LogDir:             "",
					Config:             nil,
					Arch:               "",
					OS:                 "",
				},
			},
			VolumeServers: []*spec.VolumeServerSpec{
				{
					Folders: []*spec.FolderSpec{
						{
							Folder:   ".",
							DiskType: "",
							Max:      0,
						},
					},
					Ip:                 "localhost",
					PortSsh:            2222,
					IpBind:             "",
					IpPublic:           "",
					Port:               8848,
					PortGrpc:           0,
					PortPublic:         0,
					DataCenter:         "",
					Rack:               "",
					DefaultReplication: 0,
					MetricsPort:        0,
					DeployDir:          "",
					LogDir:             "",
					Config:             nil,
					Arch:               "",
					OS:                 "",
				},
			},
			FilerServers: []*spec.FilerServerSpec{
				{
					Ip:                 "localhost",
					PortSsh:            2222,
					IpBind:             "",
					IpPublic:           "",
					Port:               8889,
					PortGrpc:           0,
					PortPublic:         0,
					DataCenter:         "",
					Rack:               "",
					DefaultReplication: 0,
					MetricsPort:        0,
					DeployDir:          "",
					LogDir:             "",
					Config:             nil,
					Arch:               "",
					OS:                 "",
				},
			},
		}

		return m.Deploy(spec)
	}

	return command
}
