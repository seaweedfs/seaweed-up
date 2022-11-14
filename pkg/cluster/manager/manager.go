package manager

import (
	"bytes"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
	"github.com/thanhpk/randstr"
)

type Manager struct {
	skipConfig bool
	skipEnable bool
	skipStart  bool
	version    string
	sudoPass   string
}

func NewManager(version string) *Manager {
	return &Manager{
		skipConfig: false,
		skipEnable: false,
		skipStart:  false,
		version:    version,
		sudoPass:   " ",
	}
}

func (m *Manager) Deploy(specification *spec.Specification) error {
	for index, masterSpec := range specification.MasterServers {
		if err := m.DeployMasterServer(specification.GlobalOptions, masterSpec, index); err != nil {
			return fmt.Errorf("deploy to master server %s:%d :%v", masterSpec.Ip, masterSpec.PortSsh, err)
		}
	}
	var masters []string
	for _, masterSpec := range specification.MasterServers {
		masters = append(masters, fmt.Sprintf("%s:%d", masterSpec.Ip, masterSpec.Port))
	}
	for index, volumeSpec := range specification.VolumeServers {
		if err := m.DeployVolumeServer(specification.GlobalOptions, masters, volumeSpec, index); err != nil {
			return fmt.Errorf("deploy to volume server %s:%d :%v", volumeSpec.Ip, volumeSpec.PortSsh, err)
		}
	}
	for index, filerSpec := range specification.FilerServers {
		if err := m.DeployFilerServer(specification.GlobalOptions, masters, filerSpec, index); err != nil {
			return fmt.Errorf("deploy to filer server %s:%d :%v", filerSpec.Ip, filerSpec.PortSsh, err)
		}
	}
	return nil
}

func (m *Manager) deployComponentInstance(op operator.CommandOperator, component string, componentInstance string, cliOptions *bytes.Buffer) error {
	info("Deploying " + componentInstance + "...")

	dir := "/tmp/seaweed-up." + randstr.String(6)

	defer op.Execute("rm -rf " + dir)

	err := op.Execute("mkdir -p " + dir + "/config")
	if err != nil {
		return fmt.Errorf("error received during installation: %s", err)
	}

	data := map[string]interface{}{
		"Component":         component,
		"ComponentInstance": componentInstance,
		"TmpDir":            dir,
		"SkipEnable":        m.skipEnable,
		"SkipStart":         m.skipStart,
		"Version":           m.version,
	}

	installScript, err := scripts.RenderScript("install.sh", data)
	if err != nil {
		return err
	}

	err = op.Upload(installScript, fmt.Sprintf("%s/install_%s.sh", dir, componentInstance), "0755")
	if err != nil {
		return fmt.Errorf("error received during upload install script: %s", err)
	}

	err = op.Upload(cliOptions, fmt.Sprintf("%s/config/%s.options", dir, component), "0755")
	if err != nil {
		return fmt.Errorf("error received during upload %s.options: %s", component, err)
	}

	info("Installing " + componentInstance + "...")
	err = op.Execute(fmt.Sprintf("cat %s/install_%s.sh | SUDO_PASS=\"%s\" sh -\n", dir, componentInstance, m.sudoPass))
	if err != nil {
		return fmt.Errorf("error received during installation: %s", err)
	}

	info("Done.")
	return nil
}

func info(message string) {
	fmt.Println("[INFO] " + message)
}
