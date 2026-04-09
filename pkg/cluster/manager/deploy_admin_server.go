package manager

import (
	"bytes"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// DeployAdminServer installs and starts a SeaweedFS admin UI component on the
// target host. It mirrors DeployFilerServer and emits options consumed by
// `weed admin -options=...`.
func (m *Manager) DeployAdminServer(masters []string, a *spec.AdminServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", a.Ip, a.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "admin"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		var buf bytes.Buffer
		a.WriteToBuffer(masters, &buf)

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
}

func (m *Manager) ResetAdminServer(a *spec.AdminServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", a.Ip, a.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "admin"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, componentInstance))
	})
}

func (m *Manager) StartAdminServer(a *spec.AdminServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", a.Ip, a.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "admin"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl start seaweed_%s.service", componentInstance))
	})
}

func (m *Manager) StopAdminServer(a *spec.AdminServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", a.Ip, a.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "admin"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl stop seaweed_%s.service", componentInstance))
	})
}
