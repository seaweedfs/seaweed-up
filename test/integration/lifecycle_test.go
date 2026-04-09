//go:build integration
// +build integration

package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestClusterLifecycle deploys a small cluster then exercises the
// stop, start and destroy lifecycle subcommands end-to-end.
func TestClusterLifecycle(t *testing.T) {
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
	host := env.hosts[0]

	// Deploy baseline cluster.
	t.Run("Deploy", func(t *testing.T) {
		out, err := env.RunSeaweedUp(
			"cluster", "deploy", "test-lifecycle",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)
		if err != nil {
			t.Fatalf("deploy failed: %v\n%s", err, out)
		}
		time.Sleep(12 * time.Second)
		if !env.VerifyMasterRunning(host, 9333) {
			t.Fatalf("master not up after deploy")
		}
	})

	// Stop the cluster and make sure nothing is listening.
	t.Run("Stop", func(t *testing.T) {
		out, err := env.RunSeaweedUp(
			"cluster", "stop",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)
		if err != nil {
			t.Fatalf("stop failed: %v\n%s", err, out)
		}
		time.Sleep(5 * time.Second)
		if env.VerifyMasterRunning(host, 9333) {
			t.Errorf("master still listening after stop")
		}
	})

	// Start again and verify the master comes back.
	t.Run("Start", func(t *testing.T) {
		out, err := env.RunSeaweedUp(
			"cluster", "start",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)
		if err != nil {
			t.Fatalf("start failed: %v\n%s", err, out)
		}
		time.Sleep(10 * time.Second)
		if !env.VerifyMasterRunning(host, 9333) {
			t.Errorf("master not listening after start")
		}
	})

	// Destroy with --remove-data and verify unit files/data dir are gone.
	t.Run("DestroyRemoveData", func(t *testing.T) {
		out, err := env.RunSeaweedUp(
			"cluster", "destroy", "test-lifecycle",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--remove-data",
			"--yes",
		)
		if err != nil {
			t.Fatalf("destroy failed: %v\n%s", err, out)
		}

		container := "seaweed-up-host1"

		// No seaweed_* unit files left behind.
		unitOut, _ := exec.Command(
			"docker", "exec", container,
			"bash", "-c", "ls /etc/systemd/system/seaweed_*.service 2>/dev/null || true",
		).CombinedOutput()
		if strings.TrimSpace(string(unitOut)) != "" {
			t.Errorf("expected no seaweed unit files, got: %s", unitOut)
		}

		// Data directory removed.
		dataOut, _ := exec.Command(
			"docker", "exec", container,
			"bash", "-c", "[ -d /opt/seaweed ] && echo present || echo absent",
		).CombinedOutput()
		if !strings.Contains(string(dataOut), "absent") {
			t.Errorf("expected /opt/seaweed to be removed, got: %s", dataOut)
		}

		// Nothing listening any more.
		if env.VerifyMasterRunning(host, 9333) {
			t.Errorf("master still listening after destroy")
		}
	})
}
