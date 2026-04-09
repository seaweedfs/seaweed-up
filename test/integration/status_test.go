//go:build integration
// +build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

// TestClusterStatusReal verifies the `cluster status` command against a real
// deployed 1-master + 1-volume + 1-filer topology and expects exit code 0.
func TestClusterStatusReal(t *testing.T) {
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

	output, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-status",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--yes",
	)
	if err != nil {
		t.Logf("Deploy output: %s", output)
		t.Fatalf("Failed to deploy cluster: %v", err)
	}

	// Give services time to come up fully.
	time.Sleep(20 * time.Second)

	t.Run("StatusTable", func(t *testing.T) {
		output, err := env.RunSeaweedUp("cluster", "status", "-f", configFile)
		t.Logf("Status output: %s", output)
		if err != nil {
			t.Fatalf("cluster status returned non-zero: %v", err)
		}
		if !strings.Contains(output, "OK") {
			t.Errorf("expected healthy OK row in status output")
		}
	})

	t.Run("StatusJSON", func(t *testing.T) {
		output, err := env.RunSeaweedUp("cluster", "status", "-f", configFile, "--json")
		t.Logf("JSON status: %s", output)
		if err != nil {
			t.Fatalf("cluster status --json failed: %v", err)
		}
		if !strings.Contains(output, "\"masters\"") {
			t.Errorf("expected masters key in JSON output")
		}
	})
}
