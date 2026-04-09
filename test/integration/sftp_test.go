//go:build integration
// +build integration

package integration

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDeploySftpGateway verifies that the SFTP gateway component can be
// deployed alongside master/volume/filer and that an SFTP client can connect
// to it to upload and list a file.
func TestDeploySftpGateway(t *testing.T) {
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
		configFile := env.GetClusterConfig("cluster-sftp.yaml")
		sshKey := env.GetSSHKeyPath()

		output, err := env.RunSeaweedUp(
			"cluster", "deploy", "test-sftp",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--yes",
		)
		if err != nil {
			t.Logf("Deploy output: %s", output)
			t.Fatalf("Failed to deploy sftp cluster: %v", err)
		}
		t.Logf("Deploy output: %s", output)
	})

	time.Sleep(20 * time.Second)

	host := env.hosts[0]
	const sftpPort = 2022

	t.Run("VerifySftpListening", func(t *testing.T) {
		addr := net.JoinHostPort(host.IP, strconv.Itoa(sftpPort))
		deadline := time.Now().Add(60 * time.Second)
		for {
			conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
			if err == nil {
				_ = conn.Close()
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("sftp port %s not listening: %v", addr, err)
			}
			time.Sleep(2 * time.Second)
		}
	})

	t.Run("SftpUploadAndList", func(t *testing.T) {
		// Install an sftp client inside host1 and talk to the gateway. This
		// keeps the test self-contained without adding a new Go dependency.
		containerName := fmt.Sprintf("seaweed-up-%s", host.Name)
		installCmd := exec.Command("docker", "exec", containerName, "bash", "-c",
			"command -v sftp >/dev/null 2>&1 || (apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y openssh-client)")
		if out, err := installCmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to install sftp client: %v: %s", err, out)
		}

		prepCmd := exec.Command("docker", "exec", containerName, "bash", "-c",
			"mkdir -p /tmp/sftp-test && echo 'hello-seaweed-sftp' > /tmp/sftp-test/hello.txt")
		if out, err := prepCmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to prepare upload file: %v: %s", err, out)
		}

		users := []string{"anonymous", "seaweed", "root"}
		var lastErr error
		var lastOut []byte
		for _, user := range users {
			batch := "put /tmp/sftp-test/hello.txt /sftp-test/hello.txt\nls /sftp-test\nquit\n"
			cmd := exec.Command("docker", "exec", "-i", containerName, "bash", "-c", fmt.Sprintf(
				"sftp -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null -oBatchMode=no -oPreferredAuthentications=password,none -P %d %s@%s",
				sftpPort, user, host.IP))
			cmd.Stdin = bytes.NewBufferString(batch)
			out, err := cmd.CombinedOutput()
			lastErr = err
			lastOut = out
			if err == nil && strings.Contains(string(out), "hello.txt") {
				t.Logf("sftp upload/list succeeded as %s:\n%s", user, out)
				return
			}
			t.Logf("sftp attempt as %s failed: %v\n%s", user, err, out)
		}
		t.Fatalf("sftp upload/list failed for all users, last error %v:\n%s", lastErr, lastOut)
	})
}
