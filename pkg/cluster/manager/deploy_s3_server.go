package manager

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/seaweedfs/seaweed-up/scripts"
	"github.com/thanhpk/randstr"
)

func (m *Manager) DeployS3Server(s *spec.S3ServerSpec, index int) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", s.Ip, s.PortSsh), m.User, m.IdentityFile, m.sudoPass, func(op operator.CommandOperator) error {

		component := "s3"
		componentInstance := fmt.Sprintf("%s%d", component, index)

		// The config directory used at runtime on the remote host.
		// Mirrors scripts/install.sh: ${CONFIG_DIR}/${COMPONENT_INSTANCE}.d
		remoteConfigDir := fmt.Sprintf("%s/%s.d", m.confDir, componentInstance)

		var s3ConfigPath string
		var s3ConfigBuf *bytes.Buffer
		if len(s.S3Config) > 0 {
			b, err := json.MarshalIndent(s.S3Config, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal s3.json: %w", err)
			}
			s3ConfigBuf = bytes.NewBuffer(b)
			s3ConfigPath = fmt.Sprintf("%s/s3.json", remoteConfigDir)
		}

		var buf bytes.Buffer
		s.WriteToBuffer(&buf, s3ConfigPath)

		return m.deployS3Instance(op, component, componentInstance, &buf, s3ConfigBuf)
	})
}

// deployS3Instance is a specialization of deployComponentInstance that also
// uploads an optional s3.json IAM config alongside s3.options.
func (m *Manager) deployS3Instance(op operator.CommandOperator, component, componentInstance string, cliOptions, s3Config *bytes.Buffer) error {
	info("Deploying " + componentInstance + "...")

	dir := "/tmp/seaweed-up." + randstr.String(6)
	defer func() { _ = op.Execute("rm -rf " + dir) }()

	if err := op.Execute("mkdir -p " + dir + "/config"); err != nil {
		return fmt.Errorf("error received during installation: %w", err)
	}

	data := map[string]interface{}{
		"Component":         component,
		"ComponentInstance": componentInstance,
		"ConfigDir":         m.confDir,
		"DataDir":           m.dataDir,
		"TmpDir":            dir,
		"SkipEnable":        m.skipEnable,
		"SkipStart":         m.skipStart,
		"ForceRestart":      m.ForceRestart,
		"Version":           m.Version,
		"ProxyConfig":       "",
	}
	if m.ProxyUrl != "" {
		data["ProxyConfig"] = "--proxy " + m.ProxyUrl
	}

	installScript, err := scripts.RenderScript("install.sh", data)
	if err != nil {
		return err
	}

	if err := op.Upload(installScript, fmt.Sprintf("%s/install_%s.sh", dir, componentInstance), "0755"); err != nil {
		return fmt.Errorf("error received during upload install script: %w", err)
	}

	if err := op.Upload(cliOptions, fmt.Sprintf("%s/config/%s.options", dir, component), "0644"); err != nil {
		return fmt.Errorf("error received during upload %s.options: %w", component, err)
	}

	if s3Config != nil {
		// s3.json contains IAM credentials; restrict to owner-only.
		if err := op.Upload(s3Config, fmt.Sprintf("%s/config/s3.json", dir), "0600"); err != nil {
			return fmt.Errorf("error received during upload s3.json: %w", err)
		}
	}

	info("Installing " + componentInstance + "...")
	if err := op.Execute(fmt.Sprintf("cat %s/install_%s.sh | SUDO_PASS=\"%s\" sh -\n", dir, componentInstance, m.sudoPass)); err != nil {
		return fmt.Errorf("error received during installation: %w", err)
	}

	info("Done.")
	return nil
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
