//go:build integration
// +build integration

package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDeployWorker deploys master+volume+filer+worker and asserts the worker
// systemd service is active on the worker host.
func TestDeployWorker(t *testing.T) {
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

	configFile := env.GetClusterConfig("cluster-worker.yaml")
	sshKey := env.GetSSHKeyPath()

	output, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-worker",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--yes",
	)
	if err != nil {
		t.Logf("Deploy output: %s", output)
		t.Fatalf("Failed to deploy cluster: %v", err)
	}
	t.Logf("Deploy output: %s", output)

	// Give services time to start.
	time.Sleep(20 * time.Second)

	workerHost := env.hosts[2]
	containerName := "seaweed-up-" + workerHost.Name

	cmd := exec.Command("docker", "exec", containerName, "systemctl", "is-active", "seaweed_worker0")
	out, err := cmd.CombinedOutput()
	state := strings.TrimSpace(string(out))
	if err != nil || state != "active" {
		t.Fatalf("seaweed_worker0 is not active on %s: state=%q err=%v", workerHost.IP, state, err)
	}
	t.Logf("seaweed_worker0 is active on %s", workerHost.IP)
}
