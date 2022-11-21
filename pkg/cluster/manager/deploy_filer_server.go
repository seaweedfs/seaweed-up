package manager

import (
	"bytes"
	"fmt"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

func (m *Manager) DeployFilerServer(masters []string, f *spec.FilerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", f.Ip, f.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "filer"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		var buf bytes.Buffer
		f.WriteToBuffer(masters, &buf)

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
}
