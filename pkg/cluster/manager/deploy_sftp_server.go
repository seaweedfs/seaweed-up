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

		// Default host key and user store paths so that the CLI flags are never
		// empty. Upstream `weed sftp` fatals if userStoreFile is empty because
		// sftpd.user.NewFileStore tries to create the file at the given path.
		if s.HostKeyPath == "" {
			s.HostKeyPath = fmt.Sprintf("%s/%s.d/ssh_host_rsa_key", m.confDir, componentInstance)
		}
		if s.AuthFile == "" {
			s.AuthFile = fmt.Sprintf("%s/%s.d/users.json", m.confDir, componentInstance)
		}

		// Ensure the config directory exists and that the host key and user
		// store files are present before `weed sftp` starts. Generate an SSH
		// host key if one is not already in place, and seed an empty JSON user
		// list so the user store loader succeeds.
		prep := fmt.Sprintf(
			"mkdir -p %s/%s.d && "+
				"(test -s %s || (command -v ssh-keygen >/dev/null 2>&1 && ssh-keygen -t rsa -b 2048 -f %s -N '' -q >/dev/null)) && "+
				"(test -s %s || echo '[]' > %s) && "+
				"chmod 600 %s %s",
			m.confDir, componentInstance,
			s.HostKeyPath, s.HostKeyPath,
			s.AuthFile, s.AuthFile,
			s.HostKeyPath, s.AuthFile,
		)
		if err := m.sudo(op, prep); err != nil {
			return fmt.Errorf("prepare sftp host key and user store: %v", err)
		}

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
