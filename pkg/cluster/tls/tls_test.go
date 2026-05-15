package tls

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchellh/go-homedir"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

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
