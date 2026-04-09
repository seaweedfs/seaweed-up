//go:build integration
// +build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeployWithTLS deploys a single-node SeaweedFS cluster with the
// --tls flag enabled, then verifies that the master HTTPS endpoint is
// reachable with the generated CA and fails closed without it.
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

	configFile := env.GetClusterConfig("cluster-single.yaml")
	sshKey := env.GetSSHKeyPath()
	clusterName := "test-tls"

	// Deploy with --tls; cert init is bundled into deploy.
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
		t.Fatalf("Failed to deploy cluster with TLS: %v", err)
	}
	t.Logf("Deploy output: %s", output)

	// Check that the local CA was persisted.
	home, _ := os.UserHomeDir()
	caPath := filepath.Join(home, ".seaweed-up", "clusters", clusterName, "certs", "ca.crt")
	if _, err := os.Stat(caPath); err != nil {
		t.Fatalf("expected local CA at %s: %v", caPath, err)
	}

	// Give the master a moment to come up.
	time.Sleep(5 * time.Second)

	masterURL := "https://172.28.0.10:9333/cluster/status"

	// With CA: expect HTTP 200.
	curlOK := exec.Command("curl", "-sk", "--cacert", caPath, "-o", "/dev/null", "-w", "%{http_code}", masterURL)
	codeBytes, _ := curlOK.CombinedOutput()
	code := strings.TrimSpace(string(codeBytes))
	if code != "200" {
		t.Errorf("expected HTTP 200 with --cacert, got %q", code)
	}

	// Without CA: should fail TLS verification (non-zero curl exit).
	curlBad := exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", masterURL)
	if err := curlBad.Run(); err == nil {
		t.Errorf("expected curl without --cacert to fail TLS verification")
	}
}
