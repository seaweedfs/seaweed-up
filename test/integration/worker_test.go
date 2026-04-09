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

	workerHost := env.hosts[2]
	containerName := "seaweed-up-" + workerHost.Name

	// Poll for the worker systemd unit to become active instead of sleeping
	// for a fixed duration, which is flaky on slow CI runners.
	var (
		state       string
		lastErr     error
		lastOut     []byte
		deadline    = time.Now().Add(60 * time.Second)
		pollTicker  = time.NewTicker(2 * time.Second)
	)
	defer pollTicker.Stop()
	for {
		cmd := exec.Command("docker", "exec", containerName, "systemctl", "is-active", "seaweed_worker0")
		lastOut, lastErr = cmd.CombinedOutput()
		state = strings.TrimSpace(string(lastOut))
		if lastErr == nil && state == "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("seaweed_worker0 did not become active on %s within 60s: state=%q err=%v out=%s", workerHost.IP, state, lastErr, string(lastOut))
		}
		<-pollTicker.C
	}
	t.Logf("seaweed_worker0 is active on %s", workerHost.IP)
}
