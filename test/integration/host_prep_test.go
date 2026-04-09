//go:build integration
// +build integration

package integration

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestHostPrep boots a single host, runs `cluster prepare -f ...`, and
// asserts that the host-prep artifacts (limits file, sysctl value) are
// present. Re-runs the command to verify idempotency.
func TestHostPrep(t *testing.T) {
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

	configFile := env.GetClusterConfig("cluster-prep.yaml")
	sshKey := env.GetSSHKeyPath()

	runPrep := func() {
		t.Helper()
		output, err := env.RunSeaweedUp(
			"cluster", "prepare", "test-prep",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)
		if err != nil {
			t.Fatalf("cluster prepare failed: %v\noutput: %s", err, output)
		}
		t.Logf("cluster prepare output:\n%s", output)
	}

	// First run: should apply all host prep steps.
	runPrep()

	assertLimitsFile(t, "seaweed-up-host1")
	assertSysctl(t, "seaweed-up-host1", "vm.max_map_count", "262144")

	// Second run: must remain successful and idempotent.
	runPrep()
	assertLimitsFile(t, "seaweed-up-host1")
	assertSysctl(t, "seaweed-up-host1", "vm.max_map_count", "262144")
}

func assertLimitsFile(t *testing.T, container string) {
	t.Helper()
	cmd := exec.Command("docker", "exec", container, "test", "-f", "/etc/security/limits.d/seaweed.conf")
	if err := cmd.Run(); err != nil {
		// Include file listing for easier debugging.
		ls, _ := exec.Command("docker", "exec", container, "ls", "-la", "/etc/security/limits.d/").CombinedOutput()
		t.Fatalf("/etc/security/limits.d/seaweed.conf missing on %s: %v\nls: %s", container, err, string(ls))
	}

	out, err := exec.Command("docker", "exec", container, "cat", "/etc/security/limits.d/seaweed.conf").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to cat limits file: %v", err)
	}
	if !strings.Contains(string(out), "nofile 1048576") {
		t.Errorf("limits file missing expected nofile entry:\n%s", string(out))
	}
}

func assertSysctl(t *testing.T, container, key, want string) {
	t.Helper()
	out, err := exec.Command("docker", "exec", container, "sysctl", "-n", key).CombinedOutput()
	if err != nil {
		t.Fatalf("sysctl -n %s failed on %s: %v\nout: %s", key, container, err, string(out))
	}
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("sysctl %s = %q, want %q", key, got, want)
	}
	fmt.Printf("verified sysctl %s=%s on %s\n", key, got, container)
}
