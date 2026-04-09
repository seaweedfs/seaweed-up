//go:build integration

package harness

import (
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// TestHarnessBoot1Host is a smoke test: boot a single ubuntu+systemd container
// via the harness, verify sshd is reachable on port 22 of its private IP, and
// exercise SSH key-based login using the harness's generated key.
func TestHarnessBoot1Host(t *testing.T) {
	h := New(t, 1)

	hosts := h.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(hosts))
	}
	host := hosts[0]

	// TCP reachability sanity check.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host.IP, strconv.Itoa(host.Port)), 5*time.Second)
	if err != nil {
		t.Fatalf("tcp dial %s:%d: %v", host.IP, host.Port, err)
	}
	_ = conn.Close()

	// Exercise key-based SSH login via the ssh CLI, proving both the keypair
	// and authorized_keys distribution worked end to end.
	out, err := exec.Command("ssh",
		"-i", h.SSHKey(),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10",
		"root@"+host.IP,
		"echo harness-ok",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh login to %s failed: %v\noutput: %s", host.IP, err, out)
	}
	if got := string(out); got == "" {
		t.Fatalf("expected non-empty ssh output, got empty string")
	}
}
