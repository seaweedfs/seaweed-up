package manager

import (
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func (m *Manager) CleanCluster(specification *spec.Specification) error {
	m.prepare(specification)

	// stop all
	if m.shouldInstall("filer") {
		for index, filerSpec := range specification.FilerServers {
			if err := m.StopFilerServer(filerSpec, index); err != nil {
				return fmt.Errorf("stop filer server %s:%d :%v", filerSpec.Ip, filerSpec.PortSsh, err)
			}
		}
	}
	if m.shouldInstall("volume") {
		for index, volumeSpec := range specification.VolumeServers {
			if err := m.StopVolumeServer(volumeSpec, index); err != nil {
				return fmt.Errorf("stop volume server %s:%d :%v", volumeSpec.Ip, volumeSpec.PortSsh, err)
			}
		}
	}
	if m.shouldInstall("master") {
		for index, masterSpec := range specification.MasterServers {
			if err := m.StopMasterServer(masterSpec, index); err != nil {
				return fmt.Errorf("stop master server %s:%d :%v", masterSpec.Ip, masterSpec.PortSsh, err)
			}
		}
	}

	// reset all
	if m.shouldInstall("master") {
		for index, masterSpec := range specification.MasterServers {
			if err := m.ResetMasterServer(masterSpec, index); err != nil {
				return fmt.Errorf("clean master server %s:%d :%v", masterSpec.Ip, masterSpec.PortSsh, err)
			}
		}
	}

	if m.shouldInstall("volume") {
		for index, volumeSpec := range specification.VolumeServers {
			if err := m.ResetVolumeServer(volumeSpec, index); err != nil {
				return fmt.Errorf("clean volume server %s:%d :%v", volumeSpec.Ip, volumeSpec.PortSsh, err)
			}
		}
	}
	if m.shouldInstall("filer") {
		for index, filerSpec := range specification.FilerServers {
			if err := m.ResetFilerServer(filerSpec, index); err != nil {
				return fmt.Errorf("clean filer server %s:%d :%v", filerSpec.Ip, filerSpec.PortSsh, err)
			}
		}
	}

	// start all
	if m.shouldInstall("master") {
		for index, masterSpec := range specification.MasterServers {
			if err := m.StartMasterServer(masterSpec, index); err != nil {
				return fmt.Errorf("start master server %s:%d :%v", masterSpec.Ip, masterSpec.PortSsh, err)
			}
		}
	}
	if m.shouldInstall("volume") {
		for index, volumeSpec := range specification.VolumeServers {
			if err := m.StartVolumeServer(volumeSpec, index); err != nil {
				return fmt.Errorf("start volume server %s:%d :%v", volumeSpec.Ip, volumeSpec.PortSsh, err)
			}
		}
	}
	if m.shouldInstall("filer") {
		for index, filerSpec := range specification.FilerServers {
			if err := m.StartFilerServer(filerSpec, index); err != nil {
				return fmt.Errorf("start filer server %s:%d :%v", filerSpec.Ip, filerSpec.PortSsh, err)
			}
		}
	}
	return nil
}
