//go:build integration
// +build integration

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDeployFilerPostgres deploys a single-node cluster whose filer
// stores metadata in an external PostgreSQL instance, then exercises
// the filer REST API to confirm writes and reads succeed end-to-end.
//
// The postgres service is expected to be reachable on the same docker
// network at 172.28.0.20:5432 (the CI workflow starts it for us).
func TestDeployFilerPostgres(t *testing.T) {
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

	configFile := env.GetClusterConfig("cluster-filer-postgres.yaml")
	sshKey := env.GetSSHKeyPath()

	output, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-filer-pg",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--yes",
	)
	if err != nil {
		t.Logf("Deploy output: %s", output)
		t.Fatalf("Failed to deploy cluster: %v", err)
	}

	filerBase := "http://172.28.0.10:8888/"
	filerURL := filerBase + "seaweed-up-test.txt"
	content := "hello from filer_postgres_test"

	// Poll the filer until it is ready to serve requests. The filer needs
	// a moment after deploy to connect to postgres and bring up its HTTP
	// listener; a fixed sleep is flaky, so retry a simple GET every 2s
	// for up to 60s before giving up.
	waitDeadline := time.Now().Add(60 * time.Second)
	for {
		readyResp, readyErr := http.Get(filerBase)
		if readyErr == nil {
			readyResp.Body.Close()
			if readyResp.StatusCode < 500 {
				break
			}
		}
		if time.Now().After(waitDeadline) {
			if readyErr != nil {
				t.Fatalf("filer did not become ready within 60s: %v", readyErr)
			}
			t.Fatalf("filer did not become ready within 60s: status %d", readyResp.StatusCode)
		}
		time.Sleep(2 * time.Second)
	}

	req, err := http.NewRequest(http.MethodPost, filerURL, strings.NewReader(content))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("filer POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("filer POST returned status %d: %s", resp.StatusCode, string(body))
	}

	getResp, err := http.Get(filerURL)
	if err != nil {
		t.Fatalf("filer GET failed: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("filer GET returned status %d", getResp.StatusCode)
	}
	got, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("filer GET read: %v", err)
	}
	if string(got) != content {
		t.Fatalf("filer GET mismatch: want %q, got %q", content, string(got))
	}
}
