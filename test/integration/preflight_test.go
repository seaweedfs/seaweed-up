//go:build integration
// +build integration

package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestPreflightDetectsPortConflict starts the 3-container environment, binds
// port 9333 on host2 before running `cluster check`, and asserts the command
// fails with a message that mentions host2 and port 9333.
func TestPreflightDetectsPortConflict(t *testing.T) {
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

	// Pre-bind port 9333 on host2 using netcat. The cluster spec in
	// preflight-conflict.yaml schedules a master on host2, so the check
	// MUST fail.
	host2 := "seaweed-up-host2"
	bindCmd := exec.Command("docker", "exec", "-d", host2, "bash", "-c",
		"nc -l 9333 >/dev/null 2>&1 &")
	if err := bindCmd.Run(); err != nil {
		t.Fatalf("failed to pre-bind 9333 on %s: %v", host2, err)
	}
	// Give nc a moment to bind.
	time.Sleep(2 * time.Second)

	output, err := env.RunSeaweedUp(
		"cluster", "check",
		"-f", env.GetClusterConfig("cluster-preflight-conflict.yaml"),
		"-u", "root",
		"--identity", env.GetSSHKeyPath(),
	)

	t.Logf("cluster check output:\n%s", output)

	if err == nil {
		t.Fatalf("expected cluster check to fail, got nil error. Output:\n%s", output)
	}

	if !strings.Contains(output, "172.28.0.11") {
		t.Errorf("expected output to mention host2 IP 172.28.0.11, got:\n%s", output)
	}
	if !strings.Contains(output, "9333") {
		t.Errorf("expected output to mention port 9333, got:\n%s", output)
	}
}
