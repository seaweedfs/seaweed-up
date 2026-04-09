//go:build integration
// +build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPersistedStateSingleNode verifies that deploying a cluster
// writes a state entry that `cluster list` can resolve by name, with
// the expected host count populated from the topology.
func TestPersistedStateSingleNode(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	if err := env.BuildSeaweedUp(); err != nil {
		t.Fatalf("Failed to build seaweed-up: %v", err)
	}

	// Point the CLI at an isolated state home so the test does not
	// pollute the developer's real ~/.seaweed-up and so listings are
	// deterministic.
	stateHome := t.TempDir()
	t.Setenv("SEAWEED_UP_HOME", stateHome)

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

	const clusterName = "test-state"
	output, err := env.RunSeaweedUp(
		"cluster", "deploy", clusterName,
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--yes",
	)
	if err != nil {
		t.Logf("Deploy output: %s", output)
		t.Fatalf("Failed to deploy cluster: %v", err)
	}

	t.Run("ListShowsCluster", func(t *testing.T) {
		out, err := env.RunSeaweedUp("cluster", "list")
		if err != nil {
			t.Logf("List output: %s", out)
			t.Fatalf("cluster list failed: %v", err)
		}
		if !strings.Contains(out, clusterName) {
			t.Errorf("expected cluster %q in list output, got:\n%s", clusterName, out)
		}
	})

	t.Run("ListJSONHasCorrectHostCount", func(t *testing.T) {
		out, err := env.RunSeaweedUp("cluster", "list", "--json")
		if err != nil {
			t.Logf("List JSON output: %s", out)
			t.Fatalf("cluster list --json failed: %v", err)
		}
		// Strip any leading non-JSON noise (color codes, banners).
		idx := strings.Index(out, "[")
		if idx < 0 {
			t.Fatalf("no JSON array in output:\n%s", out)
		}
		var entries []struct {
			Name    string   `json:"name"`
			Hosts   []string `json:"hosts"`
			Masters int      `json:"masters"`
			Volumes int      `json:"volumes"`
			Filers  int      `json:"filers"`
		}
		if err := json.Unmarshal([]byte(out[idx:]), &entries); err != nil {
			t.Fatalf("parse JSON: %v\nraw: %s", err, out)
		}
		var found bool
		for _, e := range entries {
			if e.Name != clusterName {
				continue
			}
			found = true
			// cluster-single.yaml uses a single host (172.28.0.10)
			// across master, volume, and filer. After de-dupe the
			// store should report exactly one host.
			if len(e.Hosts) != 1 {
				t.Errorf("Hosts = %v, want exactly 1 entry", e.Hosts)
			}
			if e.Masters != 1 {
				t.Errorf("Masters = %d, want 1", e.Masters)
			}
			if e.Volumes != 1 {
				t.Errorf("Volumes = %d, want 1", e.Volumes)
			}
			if e.Filers != 1 {
				t.Errorf("Filers = %d, want 1", e.Filers)
			}
		}
		if !found {
			t.Errorf("cluster %q not present in JSON list output", clusterName)
		}
	})
}
