//go:build integration
// +build integration

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// pinnedFromVersion / pinnedToVersion are the SeaweedFS versions used to
// exercise an actual upgrade path. Both are real releases; the "from" version
// is older than the "to" version.
const (
	pinnedFromVersion = "3.73"
	pinnedToVersion   = "3.80"
)

// masterVersionFromStatus fetches http://ip:port/dir/status and best-effort
// extracts the reported version string. Returns an empty string if it cannot
// be determined.
func masterVersionFromStatus(ip string, port int) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://%s:%d/dir/status", ip, port)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return string(body), nil
	}
	if v, ok := m["Version"].(string); ok {
		return v, nil
	}
	return string(body), nil
}

// waitForMasterVersionContains polls /dir/status until the reported version
// contains substr or the timeout elapses.
func waitForMasterVersionContains(ip string, port int, substr string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		v, err := masterVersionFromStatus(ip, port)
		if err == nil {
			last = v
			if strings.Contains(v, substr) {
				return v, true
			}
		}
		time.Sleep(2 * time.Second)
	}
	return last, false
}

// TestClusterUpgrade deploys a small cluster at pinnedFromVersion, then runs
// `cluster upgrade --version=<pinnedToVersion>` and asserts the master reports
// the new version via /dir/status.
func TestClusterUpgrade(t *testing.T) {
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
			t.Errorf("Failed to teardown: %v", err)
		}
	}()

	configFile := env.GetClusterConfig("cluster-single.yaml")
	sshKey := env.GetSSHKeyPath()

	// Initial deploy at pinned "from" version.
	out, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-upgrade",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--version", pinnedFromVersion,
		"--yes",
	)
	if err != nil {
		t.Fatalf("Initial deploy failed: %v\n%s", err, out)
	}
	t.Logf("Initial deploy output: %s", out)

	// Give the master a moment to come up.
	time.Sleep(10 * time.Second)

	host := env.hosts[0]
	if !env.VerifyMasterRunning(host, 9333) {
		t.Fatalf("Master not running after initial deploy on %s:9333", host.IP)
	}

	if v, ok := waitForMasterVersionContains(host.IP, 9333, pinnedFromVersion, 30*time.Second); !ok {
		t.Logf("Initial master version (did not match expected %s): %s", pinnedFromVersion, v)
	}

	beforeVersion, _ := masterVersionFromStatus(host.IP, 9333)
	t.Logf("Before upgrade, master version: %s", beforeVersion)

	// Run the upgrade.
	out, err = env.RunSeaweedUp(
		"cluster", "upgrade", "test-upgrade",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--version", pinnedToVersion,
		"--yes",
	)
	if err != nil {
		t.Fatalf("Upgrade failed: %v\n%s", err, out)
	}
	t.Logf("Upgrade output: %s", out)

	// Poll for the new version to appear in /dir/status.
	if v, ok := waitForMasterVersionContains(host.IP, 9333, pinnedToVersion, 60*time.Second); !ok {
		t.Errorf("Master did not report upgraded version %q; last seen %q", pinnedToVersion, v)
	} else {
		t.Logf("Master reports upgraded version: %s", v)
	}
}

// TestClusterUpgradeRollback attempts an upgrade to a bogus version, which
// should fail the post-upgrade health gate, and asserts that rollback leaves
// the previously installed version still reporting healthy.
func TestClusterUpgradeRollback(t *testing.T) {
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
			t.Errorf("Failed to teardown: %v", err)
		}
	}()

	configFile := env.GetClusterConfig("cluster-single.yaml")
	sshKey := env.GetSSHKeyPath()

	out, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-upgrade-rollback",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--version", pinnedFromVersion,
		"--yes",
	)
	if err != nil {
		t.Fatalf("Initial deploy failed: %v\n%s", err, out)
	}
	t.Logf("Initial deploy output: %s", out)

	time.Sleep(10 * time.Second)
	host := env.hosts[0]
	if !env.VerifyMasterRunning(host, 9333) {
		t.Fatalf("Master not running after initial deploy on %s:9333", host.IP)
	}
	beforeVersion, _ := masterVersionFromStatus(host.IP, 9333)
	t.Logf("Before bogus upgrade, master version: %s", beforeVersion)

	// Inject failure by using a clearly non-existent version. The post-upgrade
	// health gate should fail and the host should be rolled back.
	out, err = env.RunSeaweedUp(
		"cluster", "upgrade", "test-upgrade-rollback",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--version", "0.0.0-does-not-exist",
		"--rollback-on-failure=true",
		"--yes",
	)
	if err == nil {
		t.Logf("Upgrade to bogus version unexpectedly succeeded:\n%s", out)
	} else {
		t.Logf("Upgrade to bogus version failed as expected: %v\n%s", err, out)
	}

	// After rollback, the previously installed version should still be reachable.
	afterVersion, statusErr := masterVersionFromStatus(host.IP, 9333)
	t.Logf("After rollback, master version: %s (err=%v)", afterVersion, statusErr)
	if statusErr != nil {
		t.Errorf("Master /dir/status unreachable after rollback: %v", statusErr)
	}
	if beforeVersion != "" && afterVersion != "" {
		beforeMajor := strings.Split(beforeVersion, " ")[0]
		if !strings.Contains(afterVersion, beforeMajor) {
			t.Errorf("Rollback did not restore original version: before=%q after=%q", beforeVersion, afterVersion)
		}
	}
}
