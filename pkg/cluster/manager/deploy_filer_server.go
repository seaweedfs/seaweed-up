package manager

import (
	"bytes"
	"fmt"
	"path"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/filer"
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
		// Derive host-local path defaults (for example the leveldb2
		// directory) from the per-instance data directory so that
		// multiple filers co-located on the same host do not share a
		// single metadata store.
		renderOpts := filer.RenderOptions{
			InstanceDataDir: path.Join(m.dataDir, componentInstance),
		}
		tomlContent, err := backend.RenderTOML(renderOpts)
		if err != nil {
			return fmt.Errorf("render filer.toml: %w", err)
		}

		return m.deployComponentInstance(op, component, componentInstance, &buf, extraConfigFile{
			Name:    "filer.toml",
			Content: bytes.NewBufferString(tomlContent),
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
