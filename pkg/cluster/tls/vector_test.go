package tls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

func TestVectorHostsReceiveCertificates(t *testing.T) {
	s := &spec.Specification{
		MasterServers: []*spec.MasterServerSpec{{Ip: "10.0.0.1", PortSsh: 22}},
		VectorServers: []*spec.VectorServerSpec{{Ip: "10.0.0.2", PortSsh: 2222}, {Ip: "10.0.0.1", PortSsh: 22}},
	}
	hosts := AllHosts(s)
	if len(hosts) != 2 || hosts[1].Role != "vector" || hosts[1].SSHPort != 2222 || HostRole(s, "10.0.0.2") != "vector" {
		t.Fatalf("TLS hosts: %+v", hosts)
	}
	ca, key, err := GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BuildHostBundle(ca, key, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(bundle.VectorCert, bundle.VectorKey); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(bundle.VectorCert)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatal(err)
	}
}
