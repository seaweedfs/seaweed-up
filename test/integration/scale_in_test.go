//go:build integration
// +build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestClusterScaleInDrain deploys a 4-host cluster (1 master + filer, 3
// volumes), writes a file via the filer, then scales in host4 with drain
// and verifies the file is still readable.
func TestClusterScaleInDrain(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	// Expand to 4 hosts (framework default is 3).
	env.hosts = append(env.hosts, HostInfo{Name: "host4", IP: "172.28.0.13", Port: 22})

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

	configFile := env.GetClusterConfig("cluster-scale-in.yaml")
	sshKey := env.GetSSHKeyPath()

	t.Run("DeployCluster", func(t *testing.T) {
		output, err := env.RunSeaweedUp(
			"cluster", "deploy", "test-scale-in",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)
		if err != nil {
			t.Logf("Deploy output: %s", output)
			t.Fatalf("Failed to deploy cluster: %v", err)
		}
	})

	// Let services stabilize.
	time.Sleep(30 * time.Second)

	filerURL := "http://172.28.0.10:8888"

	t.Run("WriteFileViaFiler", func(t *testing.T) {
		// Use docker exec on host1 (has curl) to post via the filer.
		cmd := exec.Command("docker", "exec", "seaweed-up-host1", "bash", "-c",
			fmt.Sprintf("echo hello-scale-in > /tmp/test.txt && curl -sf -F file=@/tmp/test.txt %s/test/", filerURL))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("failed to write via filer: %v\n%s", err, out)
		}
		t.Logf("upload output: %s", out)
	})

	t.Run("ScaleInRemoveHost4", func(t *testing.T) {
		out, err := env.RunSeaweedUp(
			"cluster", "scale", "in", "test-scale-in",
			"-f", configFile,
			"--remove-node", "172.28.0.13",
			"--yes",
		)
		if err != nil {
			t.Logf("scale-in output: %s", out)
			t.Fatalf("scale-in failed: %v", err)
		}
		t.Logf("scale-in output: %s", out)
	})

	t.Run("ReadFileAfterScaleIn", func(t *testing.T) {
		// Poll filer for up to 30s to allow any lingering rebalance.
		deadline := time.Now().Add(30 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			resp, err := http.Get(filerURL + "/test/test.txt")
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode == 200 && strings.Contains(string(body), "hello-scale-in") {
					return
				}
				lastErr = fmt.Errorf("status %d body=%q", resp.StatusCode, string(body))
			} else {
				lastErr = err
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("file not retrievable after scale-in: %v", lastErr)
	})
}
