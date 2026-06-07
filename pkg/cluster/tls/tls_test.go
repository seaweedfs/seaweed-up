package tls

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mitchellh/go-homedir"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// fakeOperator records what UploadSecurityTOMLOnly executed and uploaded
// without ever opening an SSH session.
type fakeOperator struct {
	executed []string
	uploads  map[string]string
}

func newFakeOperator() *fakeOperator {
	return &fakeOperator{uploads: map[string]string{}}
}

func (f *fakeOperator) Execute(cmd string) error {
	f.executed = append(f.executed, cmd)
	return nil
}

func (f *fakeOperator) Output(cmd string) ([]byte, error) {
	f.executed = append(f.executed, cmd)
	return nil, nil
}

func (f *fakeOperator) Upload(src io.Reader, remotePath, mode string) error {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(src); err != nil {
		return err
	}
	f.uploads[remotePath] = buf.String()
	return nil
}

func (f *fakeOperator) UploadFile(path, remotePath, mode string) error {
	f.uploads[remotePath] = "file:" + path
	return nil
}

func TestGenerateCA(t *testing.T) {
	caPEM, caKeyPEM, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if len(caPEM) == 0 || len(caKeyPEM) == 0 {
		t.Fatalf("expected non-empty PEM output")
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatalf("failed to decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	if !caCert.IsCA {
		t.Fatalf("expected IsCA=true")
	}
	if caCert.Subject.CommonName != "SeaweedFS Root CA" {
		t.Fatalf("unexpected CN: %q", caCert.Subject.CommonName)
	}
}

func TestIssueCertValidatesAgainstCA(t *testing.T) {
	caPEM, caKeyPEM, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	certPEM, keyPEM, err := IssueCert(caPEM, caKeyPEM, "seaweedfs-master", []string{"127.0.0.1", "master.example.com"})
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		t.Fatalf("expected non-empty cert/key PEM")
	}

	// Parse and verify signature against CA.
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		t.Fatalf("decode leaf cert")
	}
	leaf, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	caBlock, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSName:   "master.example.com",
	}); err != nil {
		t.Fatalf("verify leaf against CA: %v", err)
	}
	if leaf.Subject.CommonName != "seaweedfs-master" {
		t.Fatalf("unexpected CN: %q", leaf.Subject.CommonName)
	}

	foundIP := false
	for _, ip := range leaf.IPAddresses {
		if ip.String() == "127.0.0.1" {
			foundIP = true
		}
	}
	if !foundIP {
		t.Fatalf("expected SAN to contain 127.0.0.1, got %v", leaf.IPAddresses)
	}
}

func TestRenderSecurityTOML(t *testing.T) {
	t.Run("with TLS includes jwt and grpc sections", func(t *testing.T) {
		out := RenderSecurityTOML("master", "write-key", "read-key", true)
		for _, want := range []string{
			"[jwt.filer_signing]",
			`key = "write-key"`,
			`key = "read-key"`,
			"[grpc.master]",
			"[grpc.volume]",
			"[grpc.filer]",
			"[grpc.client]",
			"/etc/seaweed/certs/ca.crt",
			"/etc/seaweed/certs/master.crt",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("security.toml missing %q\n---\n%s", want, out)
			}
		}
	})

	t.Run("without TLS only emits jwt sections", func(t *testing.T) {
		out := RenderSecurityTOML("filer", "write-key", "read-key", false)
		if !strings.Contains(out, "[jwt.filer_signing]") {
			t.Errorf("security.toml missing [jwt.filer_signing]\n---\n%s", out)
		}
		if strings.Contains(out, "[grpc") {
			t.Errorf("security.toml unexpectedly contains [grpc.*]\n---\n%s", out)
		}
		if strings.Contains(out, "ca.crt") {
			t.Errorf("security.toml unexpectedly references ca.crt\n---\n%s", out)
		}
	})
}

func TestLoadOrGenerateFilerSigningKey(t *testing.T) {
	// Redirect HOME so we don't pollute the user's ~/.seaweed-up.
	// homedir caches the resolved value; reset before and after so the
	// override takes effect and later tests aren't poisoned.
	t.Setenv("HOME", t.TempDir())
	homedir.Reset()
	t.Cleanup(homedir.Reset)

	cluster := "test-cluster"

	first, err := LoadOrGenerateFilerSigningKey(cluster, "write")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first == "" {
		t.Fatalf("expected non-empty key")
	}

	// Second call must return the persisted key, not a fresh one — otherwise
	// every deploy would rotate the JWT signing key and invalidate live
	// admin Bearer tokens.
	second, err := LoadOrGenerateFilerSigningKey(cluster, "write")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second != first {
		t.Errorf("expected persisted key, got fresh: %q != %q", second, first)
	}

	// Read variant uses a separate file so rotating one does not
	// invalidate the other.
	read, err := LoadOrGenerateFilerSigningKey(cluster, "read")
	if err != nil {
		t.Fatalf("read variant: %v", err)
	}
	if read == first {
		t.Errorf("expected distinct read variant, got identical key")
	}

	dir, _ := LocalClusterDir(cluster)
	for _, want := range []string{"jwt_filer_signing_write.key", "jwt_filer_signing_read.key"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("expected %s on disk: %v", want, err)
		}
	}

	if _, err := LoadOrGenerateFilerSigningKey(cluster, "bogus"); err == nil {
		t.Errorf("expected error for invalid variant")
	}
}

func TestUploadSecurityTOMLOnly(t *testing.T) {
	op := newFakeOperator()
	if err := UploadSecurityTOMLOnly(op, "filer", "write-key", "read-key", "root", ""); err != nil {
		t.Fatalf("UploadSecurityTOMLOnly: %v", err)
	}

	// The function must upload exactly one security.toml whose body
	// carries the JWT key the filer needs to register the IAM gRPC
	// service the Admin UI Users tab depends on.
	var tomlPath, tomlBody string
	for path, body := range op.uploads {
		if strings.HasSuffix(path, "/security.toml") {
			tomlPath = path
			tomlBody = body
		}
	}
	if tomlPath == "" {
		t.Fatalf("expected security.toml to be uploaded; uploads=%v", op.uploads)
	}
	if !strings.Contains(tomlBody, "[jwt.filer_signing]") {
		t.Errorf("uploaded security.toml missing [jwt.filer_signing]\n---\n%s", tomlBody)
	}
	if !strings.Contains(tomlBody, `key = "write-key"`) {
		t.Errorf("uploaded security.toml missing write-key\n---\n%s", tomlBody)
	}
	if strings.Contains(tomlBody, "[grpc") {
		t.Errorf("uploaded security.toml unexpectedly has [grpc.*]; that path is mTLS-only\n---\n%s", tomlBody)
	}

	// The install script must end up moving the temp file into the
	// canonical /etc/seaweed/security.toml so weed picks it up. The
	// runInstallScript helper base64-encodes the script and pipes it
	// through `base64 -d | sh`, so decode the payload before asserting.
	scriptRe := regexp.MustCompile(`echo (\S+) \| base64 -d \| sh`)
	var sawInstall, sawCleanup bool
	for _, cmd := range op.executed {
		if m := scriptRe.FindStringSubmatch(cmd); m != nil {
			decoded, err := base64.StdEncoding.DecodeString(m[1])
			if err != nil {
				t.Fatalf("decode install script: %v", err)
			}
			body := string(decoded)
			if strings.Contains(body, "install -o root -g root") && strings.Contains(body, DefaultRemoteSecurityTOML) {
				sawInstall = true
			}
		}
		if strings.HasPrefix(cmd, "rm -rf /tmp/seaweed-up-jwt.") {
			sawCleanup = true
		}
	}
	if !sawInstall {
		t.Errorf("expected install of security.toml into %s; commands=%v", DefaultRemoteSecurityTOML, op.executed)
	}
	if !sawCleanup {
		t.Errorf("expected /tmp cleanup; commands=%v", op.executed)
	}
}

// TestUploadSecurityTOMLOnly_PasswordlessSudo verifies the non-root,
// no-password path elevates with `sudo -n` rather than running the
// install script bare (which would fail writing into /etc/seaweed).
func TestUploadSecurityTOMLOnly_PasswordlessSudo(t *testing.T) {
	op := newFakeOperator()
	if err := UploadSecurityTOMLOnly(op, "filer", "write-key", "read-key", "chris", ""); err != nil {
		t.Fatalf("UploadSecurityTOMLOnly: %v", err)
	}
	var sawSudo bool
	for _, cmd := range op.executed {
		if strings.Contains(cmd, "base64 -d | sudo -n sh") {
			sawSudo = true
		}
		// must NOT run the script bare (no sudo) for a non-root user
		if strings.Contains(cmd, "base64 -d | sh") {
			t.Errorf("install script ran without sudo for a non-root user: %q", cmd)
		}
	}
	if !sawSudo {
		t.Errorf("expected the install script to run via `sudo -n sh`; commands=%v", op.executed)
	}
}

func TestFilerAndAdminHosts(t *testing.T) {
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1"}},
		FilerServers:  []*spec.FilerServerSpec{{Ip: "10.0.0.2", PortSsh: 22}},
		AdminServers:  []*spec.AdminServerSpec{{Ip: "10.0.0.3", PortSsh: 22}},
		VolumeServers: []*spec.VolumeServerSpec{{Ip: "10.0.0.4"}},
	}
	got := FilerAndAdminHosts(s)
	if len(got) != 2 {
		t.Fatalf("expected 2 hosts (filer + admin), got %d: %+v", len(got), got)
	}
	roles := map[string]string{}
	for _, h := range got {
		roles[h.IP] = h.Role
	}
	if roles["10.0.0.2"] != "filer" {
		t.Errorf("filer host wrong role: %v", roles)
	}
	if roles["10.0.0.3"] != "admin" {
		t.Errorf("admin host wrong role: %v", roles)
	}
	if _, hasMaster := roles["10.0.0.1"]; hasMaster {
		t.Errorf("master host should not appear: %v", roles)
	}
}
