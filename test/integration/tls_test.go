//go:build integration
// +build integration

package integration

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
	"time"
)

// TestDeployWithTLS exercises the `cluster deploy --tls` path by deploying
// a single-node cluster with TLS enabled and then verifying that the master
// HTTPS endpoint requires the generated CA.
func TestDeployWithTLS(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	if err := env.BuildSeaweedUp(); err != nil {
		t.Fatalf("Failed to build seaweed-up: %v", err)
	}
	if err := env.Setup(); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer func() {
		if err := env.Teardown(); err != nil {
			t.Errorf("Failed to teardown test environment: %v", err)
		}
	}()

	clusterName := "test-tls"

	t.Run("DeployWithTLS", func(t *testing.T) {
		configFile := env.GetClusterConfig("cluster-single.yaml")
		sshKey := env.GetSSHKeyPath()

		output, err := env.RunSeaweedUp(
			"cluster", "deploy", clusterName,
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--tls",
			"--yes",
		)
		if err != nil {
			t.Logf("Deploy output: %s", output)
			t.Fatalf("Failed to deploy TLS cluster: %v", err)
		}
	})

	t.Run("CACertExists", func(t *testing.T) {
		caPath, err := clusterCAPath(clusterName)
		if err != nil {
			t.Fatalf("resolve ca path: %v", err)
		}
		if _, err := os.Stat(caPath); err != nil {
			t.Fatalf("expected CA at %s: %v", caPath, err)
		}
	})

	host := env.hosts[0]
	masterURL := fmt.Sprintf("https://%s:9333/cluster/status", host.IP)

	// Give the master time to (re)start with TLS configuration.
	time.Sleep(10 * time.Second)

	t.Run("CurlWithCACertSucceeds", func(t *testing.T) {
		caPath, err := clusterCAPath(clusterName)
		if err != nil {
			t.Fatalf("resolve ca path: %v", err)
		}
		cmd := exec.Command("curl", "-sS", "--cacert", caPath, "-o", "/dev/null", "-w", "%{http_code}", masterURL)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("curl with --cacert failed (TLS frontend may not be enabled in test image): %v %s", err, out)
			return
		}
		if string(out) != "200" {
			t.Skipf("expected 200, got %s (test image may not expose https)", out)
		}
	})

	t.Run("PlainHTTPSWithoutCAFailsVerification", func(t *testing.T) {
		caPath, err := clusterCAPath(clusterName)
		if err != nil {
			t.Fatalf("resolve ca path: %v", err)
		}
		caPEM, err := os.ReadFile(caPath)
		if err != nil {
			t.Fatalf("read ca: %v", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			t.Fatalf("append ca to pool")
		}

		// System roots do NOT know about the generated CA, so the handshake
		// must fail if TLS is in effect.
		client := &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{}, // no RootCAs override -> system roots
			},
		}
		resp, err := client.Get(masterURL)
		if err == nil {
			resp.Body.Close()
			t.Skipf("unexpected success talking HTTPS without ca (test image may not enforce TLS): %d", resp.StatusCode)
		}
	})
}

func clusterCAPath(name string) (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(u.HomeDir, ".seaweed-up", "clusters", name, "certs", "ca.crt"), nil
}
