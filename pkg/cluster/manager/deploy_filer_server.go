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

		// Resolve the typed filer.toml backend (leveldb2 by default) and
		// stage it for deployComponentInstance, which ships everything in
		// dir/config to the host's ConfigDir/<instance>.d directory.
		backend, err := f.BackendFromConfig()
		if err != nil {
			return fmt.Errorf("filer backend config: %w", err)
		}
		tomlContent, err := backend.RenderTOML()
		if err != nil {
			return fmt.Errorf("render filer.toml: %w", err)
		}

		return m.deployComponentInstance(op, component, componentInstance, &buf, componentExtraFile{
			Name:    "filer.toml",
			Content: []byte(tomlContent),
		})

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
