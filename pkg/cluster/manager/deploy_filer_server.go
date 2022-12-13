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

func (m *Manager) ResetFilerServer(f *spec.FilerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", f.Ip, f.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "filer"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, componentInstance))
	})
}

func (m *Manager) StartFilerServer(f *spec.FilerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", f.Ip, f.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "filer"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl start seaweed_%s.service", componentInstance))
	})
}

func (m *Manager) StopFilerServer(f *spec.FilerServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", f.Ip, f.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "filer"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl stop seaweed_%s.service", componentInstance))
	})
}
