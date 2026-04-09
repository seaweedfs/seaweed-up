// Package harness provides a self-contained Docker-based integration test
// harness for the seaweed-up CLI. It boots geerlingguy/docker-ubuntu2204-ansible
// containers on a unique randomly-addressed bridge network, installs sshd and
// authorized keys, and exposes helpers for building and running the seaweed-up
// binary against those hosts.
//
// Each Harness instance claims a random /24 inside 172.29.0.0/16 (x in
// [10, 250]) to avoid collisions with the existing integration-tests workflow
// which uses 172.28.0.0/16. Tests call New() with the desired host count;
// cleanup (container stop/rm, network remove, tempdir wipe) is registered via
// t.Cleanup.
//
// Tests that use this package should set the build tag "integration" and will
// be skipped automatically when docker is unavailable or -short is passed.
package harness

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// dockerImage is the pinned systemd-in-docker image used by the harness.
// Pinning to a specific tag (instead of :latest) keeps the test environment
// reproducible and guards against upstream regressions breaking CI.
// The previously attempted date-style tag (e.g. 2024.04.01) does not exist on
// Docker Hub — only :latest is published. To keep the test environment
// reproducible we pin by immutable manifest-list digest instead of the mutable
// :latest tag. Bump this digest intentionally when a newer upstream image is
// required.
const dockerImage = "geerlingguy/docker-ubuntu2204-ansible@sha256:bbe4c56c16c57c902554b9a47833590926b7a7d4440aef3d9851473b9f7be9d4"

// Harness owns a docker bridge network plus a set of Ubuntu+systemd containers
// reachable via SSH. It is safe for a single test goroutine.
type Harness struct {
	NetworkName string
	Subnet      string // e.g. "172.29.137.0/24"
	Gateway     string // e.g. "172.29.137.1"
	octet       int    // the random third octet used for this harness
	hosts       []Host
	projectRoot string
	tmpDir      string
	sshKeyPath  string
	binaryPath  string
	cleanupOnce sync.Once
}

// Host describes a single container booted by the harness.
type Host struct {
	Name      string
	Container string
	IP        string
	Port      int
}

// usedOctets guards against two harnesses in the same process choosing the
// same third octet. Docker would also refuse overlapping subnets, but this
// gives a clearer error during parallel test development.
var (
	usedOctetsMu sync.Mutex
	usedOctets   = map[int]bool{}
)

// New constructs a Harness with hostCount running containers. It skips the
// test if docker is unavailable, if -short is set, or if the platform is not
// linux/darwin. All resources are cleaned up via t.Cleanup.
func New(t *testing.T, hostCount int) *Harness {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping harness test in -short mode")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("harness only supported on linux/darwin, got %s", runtime.GOOS)
	}
	if !dockerAvailable() {
		t.Skip("docker not available, skipping harness test")
	}
	if hostCount < 1 {
		t.Fatalf("harness.New: hostCount must be >= 1, got %d", hostCount)
	}

	root, err := findProjectRoot()
	if err != nil {
		t.Fatalf("harness.New: find project root: %v", err)
	}

	octet := pickOctet(t)
	subnet := fmt.Sprintf("172.29.%d.0/24", octet)
	gateway := fmt.Sprintf("172.29.%d.1", octet)
	// #nosec G404 -- suffix is used only to build a unique docker network/temp
	// directory name in tests; no security properties are required.
	suffix := fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Intn(100000))
	netName := "seaweedup-harness-" + suffix

	tmpDir, err := os.MkdirTemp("", "seaweedup-harness-"+suffix+"-")
	if err != nil {
		t.Fatalf("harness.New: mkdtemp: %v", err)
	}

	h := &Harness{
		NetworkName: netName,
		Subnet:      subnet,
		Gateway:     gateway,
		octet:       octet,
		projectRoot: root,
		tmpDir:      tmpDir,
	}

	// Register cleanup EARLY so any later failure still tears everything down.
	t.Cleanup(func() { h.cleanup(t) })

	for i := 0; i < hostCount; i++ {
		h.hosts = append(h.hosts, Host{
			Name:      fmt.Sprintf("host%d", i+1),
			Container: fmt.Sprintf("%s-host%d", netName, i+1),
			IP:        fmt.Sprintf("172.29.%d.%d", octet, 10+i),
			Port:      22,
		})
	}

	if err := runCmd("docker", "network", "create",
		"--driver", "bridge",
		"--subnet", subnet,
		"--gateway", gateway,
		netName); err != nil {
		t.Fatalf("harness.New: docker network create: %v", err)
	}

	for _, host := range h.hosts {
		args := []string{
			"run", "-d",
			"--name", host.Container,
			"--hostname", host.Name,
			"--privileged",
			"--cgroupns=host",
			"--network", netName,
			"--ip", host.IP,
			"-v", "/sys/fs/cgroup:/sys/fs/cgroup:rw",
			dockerImage,
		}
		if err := runCmd("docker", args...); err != nil {
			t.Fatalf("harness.New: docker run %s: %v", host.Container, err)
		}
	}

	if err := h.waitForSystemd(t); err != nil {
		t.Fatalf("harness.New: wait systemd: %v", err)
	}
	if err := h.installSSH(t); err != nil {
		t.Fatalf("harness.New: install ssh: %v", err)
	}
	if err := h.generateKey(); err != nil {
		t.Fatalf("harness.New: generate ssh key: %v", err)
	}
	if err := h.distributeKey(t); err != nil {
		t.Fatalf("harness.New: distribute ssh key: %v", err)
	}
	if err := h.waitForSSH(t); err != nil {
		t.Fatalf("harness.New: wait ssh: %v", err)
	}

	return h
}

// Hosts returns a copy of the host list for caller inspection.
func (h *Harness) Hosts() []Host {
	out := make([]Host, len(h.hosts))
	copy(out, h.hosts)
	return out
}

// SSHKey returns the path to the harness's private SSH key.
func (h *Harness) SSHKey() string { return h.sshKeyPath }

// TempDir returns a per-harness temporary directory that is removed at
// cleanup.
func (h *Harness) TempDir() string { return h.tmpDir }

// StopSSH stops the sshd service on the named host (matched against Host.Name
// or Host.Container). Useful for simulating host-reachability failures.
func (h *Harness) StopSSH(t *testing.T, name string) {
	t.Helper()
	container := h.containerFor(name)
	if container == "" {
		t.Fatalf("StopSSH: no such host %q", name)
	}
	if err := runCmd("docker", "exec", container, "bash", "-c", "systemctl stop ssh || service ssh stop || true"); err != nil {
		t.Fatalf("StopSSH: %v", err)
	}
}

// BuildBinary compiles the project's seaweed-up binary to a tempdir-scoped
// path and caches it on the Harness for subsequent Deploy calls.
func (h *Harness) BuildBinary(t *testing.T) {
	t.Helper()
	bin := filepath.Join(h.tmpDir, "seaweed-up")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = h.projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("BuildBinary: go build: %v", err)
	}
	h.binaryPath = bin
}

// Deploy invokes `seaweed-up cluster deploy -f specPath ...` against the
// harness with a test-scoped identity file. It returns the combined output
// regardless of outcome and, if the command failed, also returns the exec
// error so callers can assert on error substrings.
func (h *Harness) Deploy(t *testing.T, specPath string, extraArgs ...string) (string, error) {
	t.Helper()
	if h.binaryPath == "" {
		h.BuildBinary(t)
	}
	args := []string{
		"cluster", "deploy", "harness-cluster",
		"-f", specPath,
		"-u", "root",
		"--identity", h.sshKeyPath,
		"--yes",
	}
	args = append(args, extraArgs...)
	cmd := exec.Command(h.binaryPath, args...)
	cmd.Dir = h.projectRoot
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---- internal helpers ----

func (h *Harness) containerFor(name string) string {
	for _, host := range h.hosts {
		if host.Name == name || host.Container == name {
			return host.Container
		}
	}
	return ""
}

func (h *Harness) waitForSystemd(t *testing.T) error {
	deadline := time.Now().Add(180 * time.Second)
	for _, host := range h.hosts {
		var lastState, lastInfraErr string
		for {
			if time.Now().After(deadline) {
				// Include the most recent observed state and any non-ExitError
				// failure (docker not found, daemon down, container dead, etc.)
				// so the test output points at the actual root cause instead
				// of a bare "timeout".
				msg := fmt.Sprintf("timeout waiting for systemd on %s (last state: %q)", host.Container, lastState)
				if lastInfraErr != "" {
					msg += "; last docker error: " + lastInfraErr
				}
				return errors.New(msg)
			}
			cmd := exec.Command("docker", "exec", host.Container, "systemctl", "is-system-running")
			out, err := cmd.Output()
			state := strings.TrimSpace(string(out))
			lastState = state
			if err != nil {
				// systemctl returns non-zero while the system is still
				// initializing ("starting") or in "degraded" state. Those
				// surface as *exec.ExitError and are expected - we only log
				// real infrastructure failures (docker missing, daemon down,
				// container exited) loudly so CI output reveals the cause.
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					lastInfraErr = err.Error()
					t.Logf("waitForSystemd: docker exec on %s failed: %v (state=%q)", host.Container, err, state)
				}
			}
			if state == "running" || state == "degraded" {
				t.Logf("systemd ready on %s (%s)", host.Container, state)
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	return nil
}

func (h *Harness) installSSH(t *testing.T) error {
	for _, host := range h.hosts {
		t.Logf("installing ssh on %s", host.Container)
		steps := [][]string{
			{"bash", "-c", "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y openssh-server"},
			{"bash", "-c", "mkdir -p /run/sshd"},
			{"bash", "-c", "sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin yes/' /etc/ssh/sshd_config"},
			{"bash", "-c", "systemctl enable ssh && systemctl start ssh"},
		}
		for _, step := range steps {
			if err := runCmd("docker", append([]string{"exec", host.Container}, step...)...); err != nil {
				return fmt.Errorf("install ssh on %s: %w", host.Container, err)
			}
		}
	}
	return nil
}

func (h *Harness) generateKey() error {
	keyPath := filepath.Join(h.tmpDir, "id_rsa_harness")
	cmd := exec.Command("ssh-keygen", "-t", "rsa", "-b", "2048", "-f", keyPath, "-N", "", "-q")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh-keygen: %w", err)
	}
	h.sshKeyPath = keyPath
	return nil
}

func (h *Harness) distributeKey(t *testing.T) error {
	pub, err := os.ReadFile(h.sshKeyPath + ".pub")
	if err != nil {
		return err
	}
	pubStr := strings.TrimSpace(string(pub))
	for _, host := range h.hosts {
		setup := fmt.Sprintf(
			"mkdir -p /root/.ssh && chmod 700 /root/.ssh && echo %q > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys && chown root:root /root/.ssh/authorized_keys",
			pubStr,
		)
		if err := runCmd("docker", "exec", host.Container, "bash", "-c", setup); err != nil {
			return fmt.Errorf("authorized_keys on %s: %w", host.Container, err)
		}
		t.Logf("authorized_keys installed on %s", host.Container)
	}
	return nil
}

func (h *Harness) waitForSSH(t *testing.T) error {
	deadline := time.Now().Add(120 * time.Second)
	for _, host := range h.hosts {
		for {
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for ssh on %s (%s)", host.Container, host.IP)
			}
			addr := net.JoinHostPort(host.IP, strconv.Itoa(host.Port))
			conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
			if err == nil {
				_ = conn.Close()
				t.Logf("ssh ready on %s (%s)", host.Container, host.IP)
				break
			}
			time.Sleep(1 * time.Second)
		}
	}
	return nil
}

func (h *Harness) cleanup(t *testing.T) {
	h.cleanupOnce.Do(func() {
		for _, host := range h.hosts {
			_ = runCmd("docker", "rm", "-f", host.Container)
		}
		_ = runCmd("docker", "network", "rm", h.NetworkName)
		_ = os.RemoveAll(h.tmpDir)
		releaseOctet(h.octet)
	})
}

// ---- package helpers ----

func dockerAvailable() bool {
	return exec.Command("docker", "info").Run() == nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %v (output: %s)", name, strings.Join(args, " "), err, buf.String())
	}
	return nil
}

func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above cwd")
		}
		dir = parent
	}
}

func pickOctet(t *testing.T) int {
	t.Helper()
	usedOctetsMu.Lock()
	defer usedOctetsMu.Unlock()
	// Range [10, 250] gives 241 options; use a deterministic shuffle to
	// quickly find a free one.
	// #nosec G404 -- non-cryptographic randomness is sufficient for picking a
	// free /24 octet inside a test-only docker bridge subnet.
	r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(os.Getpid())))
	perm := r.Perm(241)
	for _, p := range perm {
		o := 10 + p
		if !usedOctets[o] {
			usedOctets[o] = true
			return o
		}
	}
	t.Fatalf("pickOctet: no free octet in 172.29.10-250")
	return 0
}

func releaseOctet(octet int) {
	usedOctetsMu.Lock()
	defer usedOctetsMu.Unlock()
	delete(usedOctets, octet)
}
