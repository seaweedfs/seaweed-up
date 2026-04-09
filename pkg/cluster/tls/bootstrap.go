package tls

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// Bootstrap describes a rendered set of CA + per-host certificates and the
// paths on the local filesystem where they have been persisted.
type Bootstrap struct {
	Dir     string
	CA      []byte
	CAKey   []byte
	Hosts   map[string]HostCerts // indexed by "component:host"
}

// HostCerts holds the leaf cert/key pair for one host+component.
type HostCerts struct {
	Component string
	Host      string
	Cert      []byte
	Key       []byte
}

// hostKey returns the map key used to look up a particular host's cert set.
func hostKey(component, host string) string {
	return component + ":" + host
}

// LocalClusterDir returns ~/.seaweed-up/clusters/<name>.
func LocalClusterDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".seaweed-up", "clusters", name), nil
}

// InitCluster generates a new CA and issues per-host certificates for every
// component in the specification. All artifacts are persisted under
// ~/.seaweed-up/clusters/<name>/certs/.
func InitCluster(name string, s *spec.Specification) (*Bootstrap, error) {
	caPEM, caKey, err := GenerateCA()
	if err != nil {
		return nil, err
	}

	return issueAllFromCA(name, s, caPEM, caKey)
}

// RotateCluster loads the existing CA from the local cluster directory and
// re-issues every per-host leaf certificate without touching the CA itself.
func RotateCluster(name string, s *spec.Specification) (*Bootstrap, error) {
	dir, err := LocalClusterDir(name)
	if err != nil {
		return nil, err
	}
	certDir := filepath.Join(dir, "certs")

	caPEM, err := os.ReadFile(filepath.Join(certDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("read existing CA cert: %w", err)
	}
	caKey, err := os.ReadFile(filepath.Join(certDir, "ca.key"))
	if err != nil {
		return nil, fmt.Errorf("read existing CA key: %w", err)
	}

	return issueAllFromCA(name, s, caPEM, caKey)
}

func issueAllFromCA(name string, s *spec.Specification, caPEM, caKey []byte) (*Bootstrap, error) {
	dir, err := LocalClusterDir(name)
	if err != nil {
		return nil, err
	}
	certDir := filepath.Join(dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		return nil, fmt.Errorf("create cert dir: %w", err)
	}

	bs := &Bootstrap{
		Dir:   certDir,
		CA:    caPEM,
		CAKey: caKey,
		Hosts: make(map[string]HostCerts),
	}

	if err := writeFileSecure(filepath.Join(certDir, "ca.crt"), caPEM); err != nil {
		return nil, err
	}
	if err := writeFileSecure(filepath.Join(certDir, "ca.key"), caKey); err != nil {
		return nil, err
	}

	type hostEntry struct {
		component string
		host      string
	}
	var entries []hostEntry
	for _, m := range s.MasterServers {
		entries = append(entries, hostEntry{"master", m.Ip})
	}
	for _, v := range s.VolumeServers {
		entries = append(entries, hostEntry{"volume", v.Ip})
	}
	for _, f := range s.FilerServers {
		entries = append(entries, hostEntry{"filer", f.Ip})
	}

	// Always emit a shared client cert under the cluster dir for use by CLI callers.
	clientCert, clientKey, err := IssueCert(caPEM, caKey, "seaweedfs-client", []string{"localhost", "127.0.0.1"})
	if err != nil {
		return nil, err
	}
	if err := writeFileSecure(filepath.Join(certDir, "client.crt"), clientCert); err != nil {
		return nil, err
	}
	if err := writeFileSecure(filepath.Join(certDir, "client.key"), clientKey); err != nil {
		return nil, err
	}
	bs.Hosts[hostKey("client", "shared")] = HostCerts{Component: "client", Host: "shared", Cert: clientCert, Key: clientKey}

	for _, e := range entries {
		sans := []string{e.host, "localhost", "127.0.0.1"}
		cert, key, err := IssueCert(caPEM, caKey, e.host, sans)
		if err != nil {
			return nil, fmt.Errorf("issue %s cert for %s: %w", e.component, e.host, err)
		}
		bs.Hosts[hostKey(e.component, e.host)] = HostCerts{
			Component: e.component,
			Host:      e.host,
			Cert:      cert,
			Key:       key,
		}
		base := filepath.Join(certDir, fmt.Sprintf("%s-%s", e.component, e.host))
		if err := writeFileSecure(base+".crt", cert); err != nil {
			return nil, err
		}
		if err := writeFileSecure(base+".key", key); err != nil {
			return nil, err
		}
	}

	return bs, nil
}

func writeFileSecure(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// DistributeHost uploads the CA cert, the per-component leaf cert/key, a
// shared client cert/key and a security.toml to a single remote host using
// the provided operator. The component argument names the primary component
// running on this host (master/volume/filer).
func DistributeHost(op operator.CommandOperator, bs *Bootstrap, component, host string) error {
	if err := op.Execute(fmt.Sprintf("mkdir -p %s", DefaultCertDir)); err != nil {
		return fmt.Errorf("mkdir remote cert dir: %w", err)
	}

	uploads := []struct {
		content []byte
		name    string
		mode    string
	}{
		{bs.CA, "ca.crt", "0644"},
	}

	hc, ok := bs.Hosts[hostKey(component, host)]
	if !ok {
		return fmt.Errorf("no certificate issued for %s on host %s", component, host)
	}
	uploads = append(uploads,
		struct {
			content []byte
			name    string
			mode    string
		}{hc.Cert, component + ".crt", "0644"},
		struct {
			content []byte
			name    string
			mode    string
		}{hc.Key, component + ".key", "0600"},
	)

	// Also drop a shared client cert so local tools on the host can speak TLS.
	if client, ok := bs.Hosts[hostKey("client", "shared")]; ok {
		uploads = append(uploads,
			struct {
				content []byte
				name    string
				mode    string
			}{client.Cert, "client.crt", "0644"},
			struct {
				content []byte
				name    string
				mode    string
			}{client.Key, "client.key", "0600"},
		)
	}

	for _, u := range uploads {
		remote := filepath.Join(DefaultCertDir, u.name)
		if err := op.Upload(bytes.NewReader(u.content), remote, u.mode); err != nil {
			return fmt.Errorf("upload %s: %w", u.name, err)
		}
	}

	sec := RenderSecurityTOML(component)
	if err := op.Upload(bytes.NewReader([]byte(sec)), DefaultSecurityTOMLPath, "0644"); err != nil {
		return fmt.Errorf("upload security.toml: %w", err)
	}
	return nil
}
