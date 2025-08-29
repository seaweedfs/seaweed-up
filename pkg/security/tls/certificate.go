package tls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// CertificateManager manages TLS certificates for SeaweedFS clusters
type CertificateManager struct {
	certsDir  string
	caConfig  *CAConfig
}

// CAConfig represents Certificate Authority configuration
type CAConfig struct {
	Organization  []string
	Country       []string
	Province      []string
	Locality      []string
	StreetAddress []string
	PostalCode    []string
	ValidityYears int
}

// CertificateInfo contains certificate and key information
type CertificateInfo struct {
	CertPath    string
	KeyPath     string
	CACertPath  string
	Certificate *x509.Certificate
	PrivateKey  *rsa.PrivateKey
}

// NewCertificateManager creates a new certificate manager
func NewCertificateManager(certsDir string, caConfig *CAConfig) *CertificateManager {
	if caConfig == nil {
		caConfig = &CAConfig{
			Organization:  []string{"SeaweedFS Cluster"},
			Country:       []string{"US"},
			Province:      []string{"CA"},
			Locality:      []string{"San Francisco"},
			ValidityYears: 10,
		}
	}
	
	return &CertificateManager{
		certsDir: certsDir,
		caConfig: caConfig,
	}
}

// InitializeCA creates a Certificate Authority for the cluster
func (cm *CertificateManager) InitializeCA(clusterName string) error {
	// Create certificates directory
	if err := os.MkdirAll(cm.certsDir, 0755); err != nil {
		return fmt.Errorf("failed to create certificates directory: %w", err)
	}

	caKeyPath := filepath.Join(cm.certsDir, "ca-key.pem")
	caCertPath := filepath.Join(cm.certsDir, "ca-cert.pem")

	// Check if CA already exists
	if _, err := os.Stat(caCertPath); err == nil {
		fmt.Printf("CA certificate already exists at %s\n", caCertPath)
		return nil
	}

	// Generate CA private key
	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate CA private key: %w", err)
	}

	// Create CA certificate template
	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  cm.caConfig.Organization,
			Country:       cm.caConfig.Country,
			Province:      cm.caConfig.Province,
			Locality:      cm.caConfig.Locality,
			StreetAddress: cm.caConfig.StreetAddress,
			PostalCode:    cm.caConfig.PostalCode,
			CommonName:    fmt.Sprintf("%s-ca", clusterName),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Duration(cm.caConfig.ValidityYears) * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Create the CA certificate
	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return fmt.Errorf("failed to create CA certificate: %w", err)
	}

	// Save CA certificate
	caCertOut, err := os.Create(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to create CA certificate file: %w", err)
	}
	defer caCertOut.Close()

	if err := pem.Encode(caCertOut, &pem.Block{Type: "CERTIFICATE", Bytes: caCertDER}); err != nil {
		return fmt.Errorf("failed to write CA certificate: %w", err)
	}

	// Save CA private key
	caKeyOut, err := os.OpenFile(caKeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create CA key file: %w", err)
	}
	defer caKeyOut.Close()

	caPrivKeyDER, err := x509.MarshalPKCS8PrivateKey(caPrivKey)
	if err != nil {
		return fmt.Errorf("failed to marshal CA private key: %w", err)
	}

	if err := pem.Encode(caKeyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: caPrivKeyDER}); err != nil {
		return fmt.Errorf("failed to write CA private key: %w", err)
	}

	fmt.Printf("CA certificate created: %s\n", caCertPath)
	fmt.Printf("CA private key created: %s\n", caKeyPath)

	return nil
}

// GenerateComponentCertificate generates a certificate for a SeaweedFS component
func (cm *CertificateManager) GenerateComponentCertificate(componentType, host string, port int, altNames []string) (*CertificateInfo, error) {
	// Load CA certificate and key
	caCert, caKey, err := cm.loadCA()
	if err != nil {
		return nil, fmt.Errorf("failed to load CA: %w", err)
	}

	// Generate component private key
	compPrivKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate component private key: %w", err)
	}

	// Prepare certificate paths
	certName := fmt.Sprintf("%s-%s", componentType, host)
	certPath := filepath.Join(cm.certsDir, fmt.Sprintf("%s-cert.pem", certName))
	keyPath := filepath.Join(cm.certsDir, fmt.Sprintf("%s-key.pem", certName))

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			Organization:  cm.caConfig.Organization,
			Country:       cm.caConfig.Country,
			Province:      cm.caConfig.Province,
			Locality:      cm.caConfig.Locality,
			CommonName:    fmt.Sprintf("%s.%s", componentType, host),
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Duration(cm.caConfig.ValidityYears) * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	// Add SANs (Subject Alternative Names)
	template.DNSNames = append(template.DNSNames, host)
	template.DNSNames = append(template.DNSNames, "localhost")
	template.DNSNames = append(template.DNSNames, fmt.Sprintf("%s.local", host))
	
	// Add custom alternative names
	for _, altName := range altNames {
		if ip := net.ParseIP(altName); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, altName)
		}
	}

	// Always add localhost and common IPs
	template.IPAddresses = append(template.IPAddresses, net.ParseIP("127.0.0.1"))
	template.IPAddresses = append(template.IPAddresses, net.ParseIP("::1"))
	
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = append(template.IPAddresses, ip)
	}

	// Create the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, &compPrivKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create component certificate: %w", err)
	}

	// Save component certificate
	certOut, err := os.Create(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate file: %w", err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return nil, fmt.Errorf("failed to write certificate: %w", err)
	}

	// Save component private key
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create key file: %w", err)
	}
	defer keyOut.Close()

	compPrivKeyDER, err := x509.MarshalPKCS8PrivateKey(compPrivKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: compPrivKeyDER}); err != nil {
		return nil, fmt.Errorf("failed to write private key: %w", err)
	}

	// Parse the created certificate for return
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse created certificate: %w", err)
	}

	return &CertificateInfo{
		CertPath:    certPath,
		KeyPath:     keyPath,
		CACertPath:  filepath.Join(cm.certsDir, "ca-cert.pem"),
		Certificate: cert,
		PrivateKey:  compPrivKey,
	}, nil
}

// GenerateClusterCertificates generates certificates for all components in a cluster
func (cm *CertificateManager) GenerateClusterCertificates(cluster *spec.Specification) (map[string]*CertificateInfo, error) {
	certificates := make(map[string]*CertificateInfo)

	// Generate certificates for master servers
	for i, master := range cluster.MasterServers {
		certInfo, err := cm.GenerateComponentCertificate("master", master.Host, master.Port, []string{})
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificate for master %d: %w", i, err)
		}
		certificates[fmt.Sprintf("master-%s", master.Host)] = certInfo
	}

	// Generate certificates for volume servers
	for i, volume := range cluster.VolumeServers {
		certInfo, err := cm.GenerateComponentCertificate("volume", volume.Host, volume.Port, []string{})
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificate for volume %d: %w", i, err)
		}
		certificates[fmt.Sprintf("volume-%s", volume.Host)] = certInfo
	}

	// Generate certificates for filer servers
	for i, filer := range cluster.FilerServers {
		altNames := []string{}
		
		// Add S3 endpoint names if S3 is enabled
		if filer.S3 {
			altNames = append(altNames, fmt.Sprintf("s3.%s", filer.Host))
			altNames = append(altNames, "s3.local")
		}

		certInfo, err := cm.GenerateComponentCertificate("filer", filer.Host, filer.Port, altNames)
		if err != nil {
			return nil, fmt.Errorf("failed to generate certificate for filer %d: %w", i, err)
		}
		certificates[fmt.Sprintf("filer-%s", filer.Host)] = certInfo
	}

	return certificates, nil
}

// loadCA loads the CA certificate and private key
func (cm *CertificateManager) loadCA() (*x509.Certificate, *rsa.PrivateKey, error) {
	caCertPath := filepath.Join(cm.certsDir, "ca-cert.pem")
	caKeyPath := filepath.Join(cm.certsDir, "ca-key.pem")

	// Load CA certificate
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Load CA private key
	caKeyPEM, err := os.ReadFile(caKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read CA private key: %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode CA private key PEM")
	}

	caKeyInterface, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA private key: %w", err)
	}

	caKey, ok := caKeyInterface.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("CA private key is not RSA")
	}

	return caCert, caKey, nil
}

// ValidateCertificate validates a certificate against the CA
func (cm *CertificateManager) ValidateCertificate(certPath string) error {
	// Load CA certificate
	caCert, _, err := cm.loadCA()
	if err != nil {
		return fmt.Errorf("failed to load CA: %w", err)
	}

	// Load certificate to validate
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read certificate: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Create certificate pool with CA
	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	// Verify the certificate
	opts := x509.VerifyOptions{Roots: roots}
	_, err = cert.Verify(opts)
	if err != nil {
		return fmt.Errorf("certificate validation failed: %w", err)
	}

	// Check if certificate is expired
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("certificate is not yet valid (valid from %v)", cert.NotBefore)
	}
	if now.After(cert.NotAfter) {
		return fmt.Errorf("certificate has expired (expired on %v)", cert.NotAfter)
	}

	return nil
}

// ListCertificates lists all certificates in the certificates directory
func (cm *CertificateManager) ListCertificates() ([]CertificateInfo, error) {
	var certificates []CertificateInfo

	entries, err := os.ReadDir(cm.certsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificates directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if filepath.Ext(entry.Name()) == ".pem" && !entry.IsDir() {
			certPath := filepath.Join(cm.certsDir, entry.Name())
			
			// Skip CA certificates and private keys
			if entry.Name() == "ca-cert.pem" || entry.Name() == "ca-key.pem" || 
			   filepath.Base(entry.Name())[len(filepath.Base(entry.Name()))-8:] == "-key.pem" {
				continue
			}

			// Load and parse certificate
			certPEM, err := os.ReadFile(certPath)
			if err != nil {
				continue
			}

			certBlock, _ := pem.Decode(certPEM)
			if certBlock == nil {
				continue
			}

			cert, err := x509.ParseCertificate(certBlock.Bytes)
			if err != nil {
				continue
			}

			// Find corresponding key file
			keyPath := certPath[:len(certPath)-len(filepath.Ext(certPath))] + "-key.pem"
			if _, err := os.Stat(keyPath); err != nil {
				keyPath = ""
			}

			certificates = append(certificates, CertificateInfo{
				CertPath:    certPath,
				KeyPath:     keyPath,
				CACertPath:  filepath.Join(cm.certsDir, "ca-cert.pem"),
				Certificate: cert,
			})
		}
	}

	return certificates, nil
}

// CleanupExpiredCertificates removes expired certificates
func (cm *CertificateManager) CleanupExpiredCertificates() error {
	certificates, err := cm.ListCertificates()
	if err != nil {
		return err
	}

	now := time.Now()
	for _, certInfo := range certificates {
		if certInfo.Certificate.NotAfter.Before(now) {
			fmt.Printf("Removing expired certificate: %s (expired: %v)\n", 
				certInfo.CertPath, certInfo.Certificate.NotAfter)
			
			os.Remove(certInfo.CertPath)
			if certInfo.KeyPath != "" {
				os.Remove(certInfo.KeyPath)
			}
		}
	}

	return nil
}
