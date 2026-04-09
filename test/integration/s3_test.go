//go:build integration
// +build integration

package integration

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestDeployS3Gateway deploys master+volume+filer+s3 and exercises the S3
// endpoint with the aws CLI.
func TestDeployS3Gateway(t *testing.T) {
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

	configFile := env.GetClusterConfig("cluster-s3.yaml")
	sshKey := env.GetSSHKeyPath()

	output, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-s3",
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

	host := env.hosts[0]
	// Wait for S3 port to come up.
	if !waitForPort(host.IP, 8333, 60*time.Second) {
		t.Fatalf("S3 gateway not listening on %s:8333", host.IP)
	}
	t.Logf("S3 gateway verified listening on %s:8333", host.IP)

	// Ensure aws CLI is available locally.
	if _, err := exec.LookPath("aws"); err != nil {
		t.Skip("aws CLI not installed on test runner; skipping S3 interaction test")
	}

	endpoint := fmt.Sprintf("http://%s:8333", host.IP)
	awsEnv := append(os.Environ(),
		"AWS_ACCESS_KEY_ID=any",
		"AWS_SECRET_ACCESS_KEY=any",
		"AWS_DEFAULT_REGION=us-east-1",
		"AWS_EC2_METADATA_DISABLED=true",
	)

	runAws := func(args ...string) (string, error) {
		full := append([]string{"--endpoint-url", endpoint, "--no-verify-ssl"}, args...)
		cmd := exec.Command("aws", full...)
		cmd.Env = awsEnv
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// Create bucket
	if out, err := runAws("s3", "mb", "s3://test"); err != nil {
		t.Fatalf("aws s3 mb failed: %v\n%s", err, out)
	}

	// Upload a file
	tmpFile := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(tmpFile, []byte("hello seaweed s3\n"), 0644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	if out, err := runAws("s3", "cp", tmpFile, "s3://test/hello.txt"); err != nil {
		t.Fatalf("aws s3 cp (upload) failed: %v\n%s", err, out)
	}

	// List
	if out, err := runAws("s3", "ls", "s3://test/"); err != nil {
		t.Fatalf("aws s3 ls failed: %v\n%s", err, out)
	} else {
		t.Logf("ls output: %s", out)
		AssertContains(t, out, "hello.txt", "expected hello.txt in listing")
	}

	// Download
	downloadPath := filepath.Join(t.TempDir(), "hello-downloaded.txt")
	if out, err := runAws("s3", "cp", "s3://test/hello.txt", downloadPath); err != nil {
		t.Fatalf("aws s3 cp (download) failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(downloadPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "hello seaweed s3\n" {
		t.Fatalf("downloaded content mismatch: got %q", string(data))
	}
}

func waitForPort(ip string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(1 * time.Second)
	}
	return false
}
