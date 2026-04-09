package manager

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"golang.org/x/crypto/ssh"
)

// generateSSHHostKey creates a fresh 2048-bit RSA SSH host key and returns
// the private key encoded in the OpenSSH private key format. The resulting
// bytes can be written directly to disk as an SSH host key file.
func generateSSHHostKey() ([]byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate rsa key: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal openssh private key: %w", err)
	}

	return pem.EncodeToMemory(block), nil
}

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

		// Ensure the config directory exists and seed an empty JSON user list
		// so the user store loader succeeds. The SSH host key is generated
		// locally in Go (see below) to avoid requiring ssh-keygen on the
		// remote host.
		prep := fmt.Sprintf(
			"mkdir -p %s/%s.d && "+
				"(test -s %s || echo '[]' > %s) && "+
				"chmod 600 %s",
			m.confDir, componentInstance,
			s.AuthFile, s.AuthFile,
			s.AuthFile,
		)
		if err := m.sudo(op, prep); err != nil {
			return fmt.Errorf("prepare sftp user store: %v", err)
		}

		// Generate the SSH host key locally and install it on the remote
		// host, so the remote does not need ssh-keygen installed. Only
		// generate a new key if one is not already in place at HostKeyPath.
		checkHostKey := fmt.Sprintf("test -s %s", s.HostKeyPath)
		if err := m.sudo(op, checkHostKey); err != nil {
			hostKey, genErr := generateSSHHostKey()
			if genErr != nil {
				return fmt.Errorf("generate sftp host key: %v", genErr)
			}
			tmpHostKey := fmt.Sprintf("/tmp/seaweed-up-%s-host-key-%d", componentInstance, index)
			if upErr := op.Upload(bytes.NewReader(hostKey), tmpHostKey, "0600"); upErr != nil {
				return fmt.Errorf("upload sftp host key: %v", upErr)
			}
			install := fmt.Sprintf(
				"mv %s %s && chmod 600 %s && rm -f %s",
				tmpHostKey, s.HostKeyPath, s.HostKeyPath, tmpHostKey,
			)
			if err := m.sudo(op, install); err != nil {
				return fmt.Errorf("install sftp host key: %v", err)
			}
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
