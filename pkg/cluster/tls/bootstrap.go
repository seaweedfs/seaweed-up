package tls

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// Bundle holds the CA and the per-component certs that should be
// installed on a single host.
type Bundle struct {
	CACert     []byte
	CAKey      []byte // only populated on the control node
	MasterCert []byte
	MasterKey  []byte
	VolumeCert []byte
	VolumeKey  []byte
	FilerCert  []byte
	FilerKey   []byte
	ClientCert []byte
	ClientKey  []byte
}

// LocalClusterDir returns the local directory where a cluster's CA and
// certs should be persisted, e.g. ~/.seaweed-up/clusters/<name>/certs/.
func LocalClusterDir(clusterName string) (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".seaweed-up", "clusters", clusterName, "certs"), nil
}

// LoadOrGenerateCA reads the CA from the cluster's local directory, or
// generates and persists a new one if none exists yet. Returns the PEM
// bytes for both the certificate and the private key.
func LoadOrGenerateCA(clusterName string) (caPEM, caKeyPEM []byte, err error) {
	dir, err := LocalClusterDir(clusterName)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create local cert dir: %w", err)
	}
	caCertPath := filepath.Join(dir, "ca.crt")
	caKeyPath := filepath.Join(dir, "ca.key")

	if certBytes, err1 := os.ReadFile(caCertPath); err1 == nil {
		if keyBytes, err2 := os.ReadFile(caKeyPath); err2 == nil {
			return certBytes, keyBytes, nil
		}
	}

	caPEM, caKeyPEM, err = GenerateCA()
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(caCertPath, caPEM, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(caKeyPath, caKeyPEM, 0o600); err != nil {
		return nil, nil, err
	}
	return caPEM, caKeyPEM, nil
}

// BuildHostBundle issues a fresh set of per-component certs for a
// single host. SANs include the host IP, "localhost" and 127.0.0.1.
func BuildHostBundle(caPEM, caKeyPEM []byte, hostIP string) (*Bundle, error) {
	b := &Bundle{CACert: caPEM}
	sans := []string{hostIP, "localhost", "127.0.0.1"}

	var err error
	if b.MasterCert, b.MasterKey, err = IssueCert(caPEM, caKeyPEM, "seaweedfs-master", sans); err != nil {
		return nil, err
	}
	if b.VolumeCert, b.VolumeKey, err = IssueCert(caPEM, caKeyPEM, "seaweedfs-volume", sans); err != nil {
		return nil, err
	}
	if b.FilerCert, b.FilerKey, err = IssueCert(caPEM, caKeyPEM, "seaweedfs-filer", sans); err != nil {
		return nil, err
	}
	if b.ClientCert, b.ClientKey, err = IssueCert(caPEM, caKeyPEM, "seaweedfs-client", sans); err != nil {
		return nil, err
	}
	return b, nil
}

// PersistHostBundle writes a host's certs into the cluster's local cert
// directory, under a per-host subdirectory.
func PersistHostBundle(clusterName, hostIP string, b *Bundle) error {
	dir, err := LocalClusterDir(clusterName)
	if err != nil {
		return err
	}
	hostDir := filepath.Join(dir, hostIP)
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return err
	}
	files := map[string][]byte{
		"ca.crt":     b.CACert,
		"master.crt": b.MasterCert,
		"master.key": b.MasterKey,
		"volume.crt": b.VolumeCert,
		"volume.key": b.VolumeKey,
		"filer.crt":  b.FilerCert,
		"filer.key":  b.FilerKey,
		"client.crt": b.ClientCert,
		"client.key": b.ClientKey,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(hostDir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// UploadBundle uploads the given bundle to the remote host using the
// provided operator. security.toml is rendered for the given component
// role and written to /etc/seaweed/security.toml.
func UploadBundle(op operator.CommandOperator, component string, b *Bundle) error {
	if err := op.Execute("mkdir -p " + DefaultRemoteCertDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", DefaultRemoteCertDir, err)
	}

	files := []struct {
		name string
		data []byte
		mode string
	}{
		{"ca.crt", b.CACert, "0644"},
		{"master.crt", b.MasterCert, "0644"},
		{"master.key", b.MasterKey, "0600"},
		{"volume.crt", b.VolumeCert, "0644"},
		{"volume.key", b.VolumeKey, "0600"},
		{"filer.crt", b.FilerCert, "0644"},
		{"filer.key", b.FilerKey, "0600"},
		{"client.crt", b.ClientCert, "0644"},
		{"client.key", b.ClientKey, "0600"},
	}
	for _, f := range files {
		remote := filepath.Join(DefaultRemoteCertDir, f.name)
		if err := op.Upload(bytes.NewReader(f.data), remote, f.mode); err != nil {
			return fmt.Errorf("upload %s: %w", f.name, err)
		}
	}

	toml := RenderSecurityTOML(component)
	if err := op.Upload(bytes.NewReader([]byte(toml)), DefaultRemoteSecurityTOML, "0644"); err != nil {
		return fmt.Errorf("upload security.toml: %w", err)
	}
	return nil
}

// HostRole returns the dominant role for a given host IP based on the
// cluster specification. Used to pick which section of security.toml
// headers on that host.
func HostRole(s *spec.Specification, ip string) string {
	for _, m := range s.MasterServers {
		if m.Ip == ip {
			return "master"
		}
	}
	for _, v := range s.VolumeServers {
		if v.Ip == ip {
			return "volume"
		}
	}
	for _, f := range s.FilerServers {
		if f.Ip == ip {
			return "filer"
		}
	}
	return "client"
}

// AllHosts returns the deduplicated list of host IPs referenced by the
// cluster spec's master/volume/filer server entries along with their
// primary role.
func AllHosts(s *spec.Specification) []HostEntry {
	seen := map[string]bool{}
	var out []HostEntry
	add := func(ip, role string, portSSH int) {
		if ip == "" || seen[ip] {
			return
		}
		seen[ip] = true
		out = append(out, HostEntry{IP: ip, Role: role, SSHPort: portSSH})
	}
	for _, m := range s.MasterServers {
		add(m.Ip, "master", m.PortSsh)
	}
	for _, v := range s.VolumeServers {
		add(v.Ip, "volume", v.PortSsh)
	}
	for _, f := range s.FilerServers {
		add(f.Ip, "filer", f.PortSsh)
	}
	return out
}

// HostEntry is a host referenced by the spec and the role we will write
// into its security.toml.
type HostEntry struct {
	IP      string
	Role    string
	SSHPort int
}
