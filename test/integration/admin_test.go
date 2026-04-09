//go:build integration
// +build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDeployAdminUI deploys master+volume+filer+admin and verifies the admin
// HTTP UI responds on the default port (23646).
func TestDeployAdminUI(t *testing.T) {
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

	t.Run("DeployCluster", func(t *testing.T) {
		configFile := env.GetClusterConfig("cluster-admin.yaml")
		sshKey := env.GetSSHKeyPath()

		output, err := env.RunSeaweedUp(
			"cluster", "deploy", "test-admin",
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
	})

	t.Run("VerifyAdminHTTP", func(t *testing.T) {
		time.Sleep(15 * time.Second)

		host := env.hosts[0]
		url := fmt.Sprintf("http://%s:23646/", host.IP)

		var lastErr error
		var body []byte
		var status int
		for i := 0; i < 30; i++ {
			resp, err := http.Get(url)
			if err != nil {
				lastErr = err
				time.Sleep(2 * time.Second)
				continue
			}
			status = resp.StatusCode
			body, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if status == http.StatusOK {
				break
			}
			lastErr = fmt.Errorf("status=%d", status)
			time.Sleep(2 * time.Second)
		}

		if status != http.StatusOK {
			t.Fatalf("admin UI not responding 200 at %s: %v", url, lastErr)
		}
		if !strings.Contains(string(body), "SeaweedFS") {
			t.Fatalf("admin UI body does not contain 'SeaweedFS': %q", string(body))
		}
		t.Logf("Admin UI verified at %s", url)
	})
}
