// Package tls provides helpers to bootstrap TLS for a SeaweedFS cluster.
//
// It can generate a self-signed CA, issue per-component leaf certificates,
// and render a security.toml configuration that points to the resulting
// certificate paths on the remote host.
package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// DefaultCertDir is the directory on remote hosts where certs are uploaded.
const DefaultCertDir = "/etc/seaweed/certs"

// DefaultSecurityTOMLPath is the remote path to the security.toml file.
const DefaultSecurityTOMLPath = "/etc/seaweed/security.toml"

// GenerateCA creates a fresh self-signed CA and returns the PEM-encoded
// certificate and private key.
func GenerateCA() (caPEM, caKeyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ca key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "SeaweedFS Root CA",
			Organization: []string{"SeaweedFS"},
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create ca cert: %w", err)
	}

	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal ca key: %w", err)
	}
	caKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return caPEM, caKeyPEM, nil
}

// IssueCert issues a new leaf certificate signed by the provided CA.
// The commonName is placed in the subject and the provided SANs are
// included as DNS names or IP addresses as appropriate.
func IssueCert(ca, caKey []byte, commonName string, sans []string) (certPEM, keyPEM []byte, err error) {
	caCert, caPrivKey, err := parseCA(ca, caKey)
	if err != nil {
		return nil, nil, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate leaf key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"SeaweedFS"},
		},
		NotBefore:   time.Now().Add(-5 * time.Minute),
		NotAfter:    time.Now().Add(2 * 365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	for _, s := range sans {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, s)
		}
	}
	if cn := strings.TrimSpace(commonName); cn != "" {
		if ip := net.ParseIP(cn); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, cn)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &leafKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create leaf cert: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyBytes, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal leaf key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return certPEM, keyPEM, nil
}

// parseCA decodes a PEM-encoded CA cert and private key.
func parseCA(caPEM, caKeyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("invalid ca certificate PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca cert: %w", err)
	}

	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid ca key PEM")
	}
	caKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca key: %w", err)
	}
	return caCert, caKey, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return n, nil
}

// RenderSecurityTOML returns the contents of security.toml for the given
// component. The component parameter is accepted for future per-component
// customization; today the same configuration is emitted for every host
// (all sections use the same cert paths on the remote host).
func RenderSecurityTOML(component string) string {
	_ = component // currently identical across components
	const dir = DefaultCertDir
	var b strings.Builder
	b.WriteString("# security.toml -- generated by seaweed-up\n")
	b.WriteString("# https://github.com/seaweedfs/seaweedfs/wiki/Security-Configuration\n\n")

	writeSection := func(section, name string) {
		fmt.Fprintf(&b, "[%s]\n", section)
		fmt.Fprintf(&b, "cert = \"%s/%s.crt\"\n", dir, name)
		fmt.Fprintf(&b, "key  = \"%s/%s.key\"\n", dir, name)
		fmt.Fprintf(&b, "ca   = \"%s/ca.crt\"\n\n", dir)
	}

	b.WriteString("[grpc]\n")
	fmt.Fprintf(&b, "ca = \"%s/ca.crt\"\n\n", dir)

	writeSection("grpc.master", "master")
	writeSection("grpc.volume", "volume")
	writeSection("grpc.filer", "filer")
	writeSection("grpc.client", "client")

	b.WriteString("[https.client]\n")
	fmt.Fprintf(&b, "enabled = true\n")
	fmt.Fprintf(&b, "cert = \"%s/client.crt\"\n", dir)
	fmt.Fprintf(&b, "key  = \"%s/client.key\"\n", dir)
	fmt.Fprintf(&b, "ca   = \"%s/ca.crt\"\n\n", dir)

	writeSection("https.master", "master")
	writeSection("https.volume", "volume")
	writeSection("https.filer", "filer")

	return b.String()
}
