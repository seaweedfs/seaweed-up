package integration

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEnvironment represents the Docker-based test environment
type TestEnvironment struct {
	t             *testing.T
	projectRoot   string
	testDataDir   string
	dockerRunning bool
	hosts         []HostInfo
}

// HostInfo contains information about a target host
type HostInfo struct {
	Name string
	IP   string
	Port int
}

// NewTestEnvironment creates a new test environment
func NewTestEnvironment(t *testing.T) *TestEnvironment {
	projectRoot := findProjectRoot()
	testDataDir := filepath.Join(projectRoot, "test", "integration", "testdata")

	env := &TestEnvironment{
		t:           t,
		projectRoot: projectRoot,
		testDataDir: testDataDir,
		hosts: []HostInfo{
			{Name: "host1", IP: "172.28.0.10", Port: 22},
			{Name: "host2", IP: "172.28.0.11", Port: 22},
			{Name: "host3", IP: "172.28.0.12", Port: 22},
		},
	}

	return env
}

// SkipIfNotAvailable skips the test if Docker is not available
func (e *TestEnvironment) SkipIfNotAvailable(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker is not available, skipping integration test")
	}
}

// Setup starts the Docker environment
func (e *TestEnvironment) Setup() error {
	e.t.Log("Setting up Docker test environment...")

	composeFile := filepath.Join(e.projectRoot, "test", "integration", "docker-compose.yml")

	// Start Docker Compose
	cmd := exec.Command("docker", "compose", "-f", composeFile, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start Docker Compose: %w", err)
	}

	e.dockerRunning = true

	// Wait for systemd to be ready in containers
	if err := e.waitForSystemd(); err != nil {
		return fmt.Errorf("failed waiting for systemd: %w", err)
	}

	// Install SSH on containers
	if err := e.installSSH(); err != nil {
		return fmt.Errorf("failed to install SSH: %w", err)
	}

	// Wait for SSH to be ready
	if err := e.waitForHosts(); err != nil {
		return fmt.Errorf("failed waiting for SSH: %w", err)
	}

	// Setup SSH keys on hosts
	if err := e.setupSSHKeys(); err != nil {
		return fmt.Errorf("failed to setup SSH keys: %w", err)
	}

	e.t.Log("Docker test environment ready!")
	return nil
}

// Teardown stops and removes the Docker environment
func (e *TestEnvironment) Teardown() error {
	if !e.dockerRunning {
		return nil
	}

	e.t.Log("Tearing down Docker test environment...")

	composeFile := filepath.Join(e.projectRoot, "test", "integration", "docker-compose.yml")

	cmd := exec.Command("docker", "compose", "-f", composeFile, "down", "-v", "--remove-orphans")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop Docker Compose: %w", err)
	}

	e.dockerRunning = false
	e.t.Log("Docker test environment cleaned up!")
	return nil
}

// waitForSystemd waits for systemd to be running in all containers
func (e *TestEnvironment) waitForSystemd() error {
	e.t.Log("Waiting for systemd to be ready in containers...")

	timeout := 180 * time.Second
	interval := 3 * time.Second
	deadline := time.Now().Add(timeout)

	for _, host := range e.hosts {
		containerName := fmt.Sprintf("seaweed-up-%s", host.Name)
		for {
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for systemd on %s", containerName)
			}

			cmd := exec.Command("docker", "exec", containerName, "systemctl", "is-system-running", "--quiet")
			if err := cmd.Run(); err == nil {
				e.t.Logf("Systemd ready on %s", containerName)
				break
			}

			// Also accept "degraded" state (some services failed but systemd is running)
			cmd = exec.Command("docker", "exec", containerName, "systemctl", "is-system-running")
			output, _ := cmd.Output()
			state := strings.TrimSpace(string(output))
			if state == "running" || state == "degraded" {
				e.t.Logf("Systemd ready on %s (state: %s)", containerName, state)
				break
			}

			e.t.Logf("Waiting for systemd on %s (state: %s)...", containerName, state)
			time.Sleep(interval)
		}
	}

	return nil
}

// installSSH installs and configures SSH on all containers
func (e *TestEnvironment) installSSH() error {
	e.t.Log("Installing SSH on containers...")

	for _, host := range e.hosts {
		containerName := fmt.Sprintf("seaweed-up-%s", host.Name)
		e.t.Logf("Installing SSH on %s...", containerName)

		// Install SSH
		cmd := exec.Command("docker", "exec", containerName, "bash", "-c",
			"apt-get update && apt-get install -y openssh-server curl netcat-openbsd")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install SSH on %s: %w", containerName, err)
		}

		// Configure SSH
		commands := []string{
			"mkdir -p /run/sshd",
			"sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config",
			"sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config",
			"systemctl enable ssh && systemctl start ssh",
		}

		for _, cmdStr := range commands {
			cmd := exec.Command("docker", "exec", containerName, "bash", "-c", cmdStr)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to configure SSH on %s: %w", containerName, err)
			}
		}

		e.t.Logf("SSH installed on %s", containerName)
	}

	// Give SSH a moment to fully start
	time.Sleep(3 * time.Second)
	return nil
}

// waitForHosts waits for all target hosts to be ready (SSH accessible)
func (e *TestEnvironment) waitForHosts() error {
	e.t.Log("Waiting for target hosts to be ready...")

	timeout := 120 * time.Second
	interval := 2 * time.Second
	deadline := time.Now().Add(timeout)

	for _, host := range e.hosts {
		for {
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for host %s (%s:%d)", host.Name, host.IP, host.Port)
			}

			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host.IP, host.Port), 5*time.Second)
			if err == nil {
				conn.Close()
				e.t.Logf("Host %s (%s) is ready", host.Name, host.IP)
				break
			}

			e.t.Logf("Waiting for host %s (%s)...", host.Name, host.IP)
			time.Sleep(interval)
		}
	}

	return nil
}

// setupSSHKeys generates and copies SSH keys to target hosts
func (e *TestEnvironment) setupSSHKeys() error {
	e.t.Log("Setting up SSH keys on target hosts...")

	// Create a temporary SSH key for testing
	keyDir := filepath.Join(e.projectRoot, "test", "integration", ".ssh")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return fmt.Errorf("failed to create SSH key directory: %w", err)
	}

	keyPath := filepath.Join(keyDir, "id_rsa_test")

	// Generate SSH key if it doesn't exist
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		cmd := exec.Command("ssh-keygen", "-t", "rsa", "-b", "2048", "-f", keyPath, "-N", "", "-q")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to generate SSH key: %w", err)
		}
	}

	// Read public key
	pubKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return fmt.Errorf("failed to read public key: %w", err)
	}

	// Copy public key to each host using docker exec
	for _, host := range e.hosts {
		containerName := fmt.Sprintf("seaweed-up-%s", host.Name)
		setupCmd := fmt.Sprintf(
			"mkdir -p /root/.ssh && chmod 700 /root/.ssh && echo '%s' > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys && chown root:root /root/.ssh/authorized_keys",
			strings.TrimSpace(string(pubKey)),
		)
		cmd := exec.Command("docker", "exec", containerName, "bash", "-c", setupCmd)

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to copy SSH key to %s: %w", host.Name, err)
		}
		e.t.Logf("SSH key copied to %s", host.Name)
	}

	return nil
}

// GetSSHKeyPath returns the path to the test SSH private key
func (e *TestEnvironment) GetSSHKeyPath() string {
	return filepath.Join(e.projectRoot, "test", "integration", ".ssh", "id_rsa_test")
}

// GetClusterConfig returns the path to a test cluster configuration
func (e *TestEnvironment) GetClusterConfig(name string) string {
	return filepath.Join(e.testDataDir, name)
}

// GetSeaweedUpBinary returns the path to the seaweed-up binary
func (e *TestEnvironment) GetSeaweedUpBinary() string {
	return filepath.Join(e.projectRoot, "seaweed-up")
}

// BuildSeaweedUp builds the seaweed-up binary
func (e *TestEnvironment) BuildSeaweedUp() error {
	e.t.Log("Building seaweed-up binary...")

	cmd := exec.Command("go", "build", "-o", e.GetSeaweedUpBinary(), ".")
	cmd.Dir = e.projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build seaweed-up: %w", err)
	}

	e.t.Log("seaweed-up binary built successfully!")
	return nil
}

// RunSeaweedUp runs the seaweed-up command with the given arguments
func (e *TestEnvironment) RunSeaweedUp(args ...string) (string, error) {
	binary := e.GetSeaweedUpBinary()

	cmd := exec.Command(binary, args...)
	cmd.Dir = e.projectRoot
	output, err := cmd.CombinedOutput()

	return string(output), err
}

// VerifyMasterRunning checks if master server is running on a host
func (e *TestEnvironment) VerifyMasterRunning(host HostInfo, port int) bool {
	addr := fmt.Sprintf("%s:%d", host.IP, port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// VerifyVolumeRunning checks if volume server is running on a host
func (e *TestEnvironment) VerifyVolumeRunning(host HostInfo, port int) bool {
	addr := fmt.Sprintf("%s:%d", host.IP, port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// VerifyFilerRunning checks if filer server is running on a host
func (e *TestEnvironment) VerifyFilerRunning(host HostInfo, port int) bool {
	addr := fmt.Sprintf("%s:%d", host.IP, port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Helper functions

func findProjectRoot() string {
	// Try to find project root by looking for go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func isDockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

// AssertNoError fails the test if err is not nil
func AssertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

// AssertContains checks if a string contains a substring
func AssertContains(t *testing.T, s, substr, msg string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%s: expected '%s' to contain '%s'", msg, s, substr)
	}
}

