package manager

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// vectorComponent is the S3 Vectors engine, weed-vector.
const vectorComponent = "vector"

// vectorComponentInstance returns the instance name, e.g. "vector0".
func vectorComponentInstance(index int) string {
	return fmt.Sprintf("%s%d", vectorComponent, index)
}

func (m *Manager) runVectorRemote(v *spec.VectorServerSpec, fn func(op operator.CommandOperator) error) error {
	return operator.ExecuteRemote(fmt.Sprintf("%s:%d", v.Ip, v.PortSsh), m.User, m.IdentityFile, m.sudoPass, fn)
}

// DeployVectorServer installs and starts weed-vector. Unlike the Lance worker,
// which rides along on worker hosts, the engine is asked for by name, so a
// host or release that cannot run it fails the deploy.
func (m *Manager) DeployVectorServer(v *spec.VectorServerSpec, index int) error {
	return m.runVectorRemote(v, func(op operator.CommandOperator) error {
		return m.deployVectorInstance(op, v, index)
	})
}

func (m *Manager) deployVectorInstance(op operator.CommandOperator, v *spec.VectorServerSpec, index int) error {
	if v.Masters == "" {
		return fmt.Errorf("vector %s: no masters (master_servers is empty and vector_servers[].masters is unset)", v.Ip)
	}
	uname, err := op.Output("uname -s -m")
	if err != nil {
		return fmt.Errorf("vector %s: detect the host platform: %w", v.Ip, err)
	}
	arch, reason := rustAssetArch("weed-vector", string(uname))
	if reason != "" {
		return fmt.Errorf("vector %s: %s", v.Ip, reason)
	}
	owner, repo := m.ReleaseOwnerRepo()
	asset := fmt.Sprintf("weed-vector_linux_%s.tar.gz", arch)
	assetURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, m.Version, asset)
	switch status, err := releaseAssetHead(assetURL); {
	case err != nil:
		return fmt.Errorf("check the weed-vector release asset %s: %w", assetURL, err)
	case status == http.StatusNotFound:
		return fmt.Errorf("vector %s: release %s does not ship %s", v.Ip, m.Version, asset)
	case status != http.StatusOK:
		return fmt.Errorf("check the weed-vector release asset %s: HTTP %d", assetURL, status)
	}
	var buf bytes.Buffer
	m.vectorOptions(v, index, &buf)
	return m.deployComponentInstance(op, vectorComponent, vectorComponentInstance(index), &buf)
}

// vectorOptions writes weed-vector's flags for deploys and upgrades alike.
// The enterprise engine keeps its id in --state-dir, so a restarted engine
// gets its placements back; its default is under /tmp, which a reboot may
// clear. The open-source engine has no such flag.
func (m *Manager) vectorOptions(v *spec.VectorServerSpec, index int, buf *bytes.Buffer) {
	v.WriteToBuffer(buf)
	if _, set := v.Config["state-dir"]; m.Enterprise && !set {
		fmt.Fprintf(buf, "state-dir=%s/%s\n", m.dataDir, vectorComponentInstance(index))
	}
}

func (m *Manager) runVectorSystemctl(v *spec.VectorServerSpec, index int, action string) error {
	return m.runVectorRemote(v, func(op operator.CommandOperator) error {
		return m.sudo(op, fmt.Sprintf("systemctl %s seaweed_%s.service", action, vectorComponentInstance(index)))
	})
}

func (m *Manager) ResetVectorServer(v *spec.VectorServerSpec, index int) error {
	return m.runVectorRemote(v, func(op operator.CommandOperator) error {
		return m.sudo(op, fmt.Sprintf("rm -Rf %s/%s/*", m.dataDir, vectorComponentInstance(index)))
	})
}

func (m *Manager) StartVectorServer(v *spec.VectorServerSpec, index int) error {
	return m.runVectorSystemctl(v, index, "start")
}

func (m *Manager) StopVectorServer(v *spec.VectorServerSpec, index int) error {
	return m.runVectorSystemctl(v, index, "stop")
}
