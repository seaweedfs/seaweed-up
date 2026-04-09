package tls

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
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
	out := RenderSecurityTOML("master")
	for _, want := range []string{
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
}
