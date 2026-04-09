package manager

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

func (m *Manager) DeployS3Server(s *spec.S3ServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "s3"
		componentInstance := fmt.Sprintf("%s%d", component, index)

		// The config directory used at runtime on the remote host.
		// Mirrors scripts/install.sh: ${CONFIG_DIR}/${COMPONENT_INSTANCE}.d
		remoteConfigDir := fmt.Sprintf("%s/%s.d", m.confDir, componentInstance)

		var s3ConfigPath string
		var extras []extraConfigFile
		if len(s.S3Config) > 0 {
			b, err := json.MarshalIndent(s.S3Config, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal s3.json: %w", err)
			}
			s3ConfigPath = fmt.Sprintf("%s/s3.json", remoteConfigDir)
			// s3.json contains IAM credentials; restrict to owner-only.
			extras = append(extras, extraConfigFile{
				Name:    "s3.json",
				Content: bytes.NewBuffer(b),
				Mode:    "0600",
			})
		}

		var buf bytes.Buffer
		s.WriteToBuffer(&buf, s3ConfigPath)

		return m.deployComponentInstance(op, component, componentInstance, &buf, extras...)
	})
}

// runS3Systemctl connects to the remote host for the given S3 spec and runs
// `systemctl <action> seaweed_s3<index>.service` via sudo. It is shared by
// Start/Stop; Reset uses a similar shape but runs `rm -Rf`.
func (m *Manager) runS3Systemctl(s *spec.S3ServerSpec, index int, action string) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		componentInstance := fmt.Sprintf("s3%d", index)
		return m.sudo(op, fmt.Sprintf("systemctl %s seaweed_%s.service", action, componentInstance))
	})
}

func (m *Manager) ResetS3Server(s *spec.S3ServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {
		componentInstance := fmt.Sprintf("s3%d", index)
		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, componentInstance))
	})
}

func (m *Manager) StartS3Server(s *spec.S3ServerSpec, index int) error {
	return m.runS3Systemctl(s, index, "start")
}

func (m *Manager) StopS3Server(s *spec.S3ServerSpec, index int) error {
	return m.runS3Systemctl(s, index, "stop")
}
