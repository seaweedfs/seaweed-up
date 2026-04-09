package tls

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"github.com/thanhpk/randstr"
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

	// If both the CA cert and key already exist on disk, reuse them.
	certBytes, certErr := os.ReadFile(caCertPath)
	keyBytes, keyErr := os.ReadFile(caKeyPath)
	if certErr == nil && keyErr == nil {
		return certBytes, keyBytes, nil
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
	ca, err := ParseCA(caPEM, caKeyPEM)
	if err != nil {
		return nil, err
	}
	return BuildHostBundleFromParsed(caPEM, ca, hostIP)
}

// BuildHostBundleFromParsed is like BuildHostBundle but accepts a
// pre-parsed CA so the caller can amortize the parse cost across many
// hosts.
func BuildHostBundleFromParsed(caPEM []byte, ca *ParsedCA, hostIP string) (*Bundle, error) {
	b := &Bundle{CACert: caPEM}
	sans := []string{hostIP, "localhost", "127.0.0.1"}

	var err error
	if b.MasterCert, b.MasterKey, err = IssueCertFromParsed(ca, "seaweedfs-master", sans); err != nil {
		return nil, err
	}
	if b.VolumeCert, b.VolumeKey, err = IssueCertFromParsed(ca, "seaweedfs-volume", sans); err != nil {
		return nil, err
	}
	if b.FilerCert, b.FilerKey, err = IssueCertFromParsed(ca, "seaweedfs-filer", sans); err != nil {
		return nil, err
	}
	if b.ClientCert, b.ClientKey, err = IssueCertFromParsed(ca, "seaweedfs-client", sans); err != nil {
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
//
// Because /etc/seaweed is typically owned by root, the bundle files are
// first uploaded into a temporary directory under /tmp (which the SSH
// user can write to) and then moved into place via a sudo-wrapped
// install script. sudoPass may be empty when the SSH user is root or
// has passwordless sudo: in that case commands are executed directly
// without any sudo wrapping.
//
// sshUser is the SSH login user; when it is "root" we skip the sudo
// wrapping entirely because the SSH session already runs as root (and
// the remote box may not even have sudo installed).
func UploadBundle(op operator.CommandOperator, component string, b *Bundle, sshUser, sudoPass string) error {
	tmpDir := "/tmp/seaweed-up-tls." + randstr.String(6)
	defer func() { _ = op.Execute("rm -rf " + tmpDir) }()

	if err := op.Execute("mkdir -p " + tmpDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", tmpDir, err)
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
		remote := filepath.Join(tmpDir, f.name)
		if err := op.Upload(bytes.NewReader(f.data), remote, f.mode); err != nil {
			return fmt.Errorf("upload %s: %w", f.name, err)
		}
	}

	toml := RenderSecurityTOML(component)
	tomlPath := filepath.Join(tmpDir, "security.toml")
	if err := op.Upload(bytes.NewReader([]byte(toml)), tomlPath, "0644"); err != nil {
		return fmt.Errorf("upload security.toml: %w", err)
	}

	// Build an install script that runs under sudo to create the final
	// directories and move files into /etc/seaweed with root ownership
	// and the correct modes. Using `install` avoids a separate chmod and
	// gives us atomic permission setting.
	var script strings.Builder
	script.WriteString("set -e\n")
	fmt.Fprintf(&script, "mkdir -p %s\n", DefaultRemoteCertDir)
	fmt.Fprintf(&script, "chmod 0755 %s\n", DefaultRemoteCertDir)
	for _, f := range files {
		src := filepath.Join(tmpDir, f.name)
		dst := filepath.Join(DefaultRemoteCertDir, f.name)
		fmt.Fprintf(&script, "install -o root -g root -m %s %s %s\n", f.mode, src, dst)
	}
	fmt.Fprintf(&script, "install -o root -g root -m 0644 %s %s\n", tomlPath, DefaultRemoteSecurityTOML)

	if err := runInstallScript(op, script.String(), sshUser, sudoPass); err != nil {
		return fmt.Errorf("install cert bundle: %w", err)
	}
	return nil
}

// runInstallScript executes the given shell script on the remote host,
// choosing between direct execution and a sudo wrapper based on the
// SSH user and whether a sudo password was supplied.
//
//   - When the SSH user is root, the script is piped straight into `sh`:
//     the session already has root privileges and the host may not even
//     have sudo installed.
//   - When sudoPass is empty, we assume NOPASSWD sudo is configured or
//     the caller is running as root in another guise, and we execute
//     without sudo. (The caller should pass a non-empty sudoPass only
//     when sudo is actually required.)
//   - Otherwise we pipe the password into `sudo -S`. sudoPass is
//     single-quote-escaped via shellSingleQuote so passwords containing
//     single quotes cannot break out of the quoted literal.
func runInstallScript(op operator.CommandOperator, script, sshUser, sudoPass string) error {
	// base64-encode the script so quoting is robust and we can embed it
	// safely inside the remote shell command.
	encoded := base64Encode([]byte(script))

	if sshUser == "root" || sudoPass == "" {
		cmd := fmt.Sprintf("echo %s | base64 -d | sh", encoded)
		return op.Execute(cmd)
	}

	// sudo -S reads the password from stdin; prepend it (safely quoted),
	// then feed the decoded script on the same pipe. op.Execute does not
	// expose a distinct stdin, so we compose the stream entirely in the
	// remote shell command.
	cmd := fmt.Sprintf("(echo %s; echo %s | base64 -d) | sudo -S -p '' sh",
		shellSingleQuote(sudoPass), encoded)
	return op.Execute(cmd)
}

// shellSingleQuote wraps s in single quotes so it is safe to embed in a
// POSIX shell command. Any single quote inside s is escaped by closing
// the quoted string, inserting an escaped quote, and reopening:
// ' -> '\''. Mirrors the helper in pkg/cluster/manager so the two
// deploy paths share the same quoting semantics.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
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
