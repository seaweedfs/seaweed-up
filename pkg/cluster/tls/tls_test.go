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
		t.Fatal("GenerateCA returned empty material")
	}

	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatal("invalid CA PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	if !cert.IsCA {
		t.Fatal("expected IsCA=true")
	}
	if cert.Subject.CommonName == "" {
		t.Fatal("expected CA to have CommonName")
	}
}

func TestIssueCertAndValidateChain(t *testing.T) {
	caPEM, caKeyPEM, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	leafPEM, leafKeyPEM, err := IssueCert(caPEM, caKeyPEM, "master-1.example.com", []string{"10.0.0.1", "master-1"})
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}
	if len(leafKeyPEM) == 0 {
		t.Fatal("empty key")
	}

	caBlock, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}

	leafBlock, _ := pem.Decode(leafPEM)
	leafCert, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	if _, err := leafCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSName:   "master-1.example.com",
	}); err != nil {
		t.Fatalf("verify leaf: %v", err)
	}

	// Ensure IP SAN present.
	foundIP := false
	for _, ip := range leafCert.IPAddresses {
		if ip.String() == "10.0.0.1" {
			foundIP = true
		}
	}
	if !foundIP {
		t.Fatalf("expected IP SAN 10.0.0.1, got %v", leafCert.IPAddresses)
	}
}

func TestRenderSecurityTOML(t *testing.T) {
	out := RenderSecurityTOML("master")
	mustContain := []string{
		"[grpc.master]",
		"[grpc.volume]",
		"[grpc.filer]",
		"[grpc.client]",
		"/etc/seaweed/certs/master.crt",
		"/etc/seaweed/certs/ca.crt",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("security.toml missing %q\n%s", s, out)
		}
	}
}
