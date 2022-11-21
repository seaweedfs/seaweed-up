package manager

import (
	"bytes"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/pkg/utils"
	"github.com/seaweedfs/seaweed-up/scripts"
	"github.com/thanhpk/randstr"
)

type Manager struct {
	User         string // username to login to the SSH server
	IdentityFile string // path to the private key file
	UsePassword  bool   // use password instead of identity file for ssh connection

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
		sudoPass:   "",
	}
}

func (m *Manager) Deploy(specification *spec.Specification) error {
	if m.UsePassword {
		password := utils.PromptForPassword("Input SSH password: ")
		m.sudoPass = password
		println()
	} else if m.User != "root" {
		password := utils.PromptForPassword("Input sudo password: ")
		m.sudoPass = password
		println()
	}
	m.User = utils.Nvl(m.User, specification.GlobalOptions.User)
	for index, masterSpec := range specification.MasterServers {
		if err := m.DeployMasterServer(masterSpec, index); err != nil {
			fmt.Printf("error is %v\n", err)
			return fmt.Errorf("deploy to master server %s:%d :%v", masterSpec.Ip, masterSpec.PortSsh, err)
		}
	}
	var masters []string
	for _, masterSpec := range specification.MasterServers {
		masters = append(masters, fmt.Sprintf("%s:%d", masterSpec.Ip, masterSpec.Port))
	}
	for index, volumeSpec := range specification.VolumeServers {
		if err := m.DeployVolumeServer(masters, volumeSpec, index); err != nil {
			return fmt.Errorf("deploy to volume server %s:%d :%v", volumeSpec.Ip, volumeSpec.PortSsh, err)
		}
	}
	for index, filerSpec := range specification.FilerServers {
		if err := m.DeployFilerServer(masters, filerSpec, index); err != nil {
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
