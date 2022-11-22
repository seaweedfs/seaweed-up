package manager

import (
	"bytes"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

func (m *Manager) DeployMasterServer(masters []string, masterSpec *spec.MasterServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", masterSpec.Ip, masterSpec.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "master"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		var buf bytes.Buffer
		masterSpec.WriteToBuffer(masters, &buf)

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
}
