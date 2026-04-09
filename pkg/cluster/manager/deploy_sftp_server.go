package manager

import (
	"bytes"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

func (m *Manager) DeploySftpServer(masters []string, s *spec.SftpServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "sftp"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		var buf bytes.Buffer
		s.WriteToBuffer(masters, &buf)

		return m.deployComponentInstance(op, component, componentInstance, &buf)

	})
}

func (m *Manager) ResetSftpServer(s *spec.SftpServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "sftp"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, componentInstance))
	})
}

func (m *Manager) StartSftpServer(s *spec.SftpServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "sftp"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl start seaweed_%s.service", componentInstance))
	})
}

func (m *Manager) StopSftpServer(s *spec.SftpServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		component := "sftp"
		componentInstance := fmt.Sprintf("%s%d", component, index)
		return m.sudo(op, fmt.Sprintf("systemctl stop seaweed_%s.service", componentInstance))
	})
}
