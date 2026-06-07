package operator

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func TestValidHostKeyPolicy(t *testing.T) {
	for _, p := range []string{"", "ignore", "accept-new", "strict"} {
		if !ValidHostKeyPolicy(p) {
			t.Errorf("expected %q to be valid", p)
		}
	}
	for _, p := range []string{"yes", "no", "STRICT", "tofu"} {
		if ValidHostKeyPolicy(p) {
			t.Errorf("expected %q to be invalid", p)
		}
	}
}

func TestHostKeyPolicyDefault(t *testing.T) {
	t.Cleanup(func() { SetHostKeyPolicy("") })
	if got := currentHostKeyPolicy(); got != HostKeyIgnore {
		t.Fatalf("default policy = %q, want %q", got, HostKeyIgnore)
	}
	SetHostKeyPolicy("strict")
	if got := currentHostKeyPolicy(); got != HostKeyStrict {
		t.Fatalf("policy = %q, want strict", got)
	}
	SetHostKeyPolicy("")
	if got := currentHostKeyPolicy(); got != HostKeyIgnore {
		t.Fatalf("reset policy = %q, want ignore", got)
	}
}

func TestKnownHostsVerifier_AcceptNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	old := knownHostsFile
	knownHostsFile = path
	t.Cleanup(func() { knownHostsFile = old })

	key := testHostKey(t)
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 22}

	cb, err := knownHostsVerifier(true)
	if err != nil {
		t.Fatal(err)
	}
	// Unknown host: accept-new learns and accepts it.
	if err := cb("10.0.0.2:22", remote, key); err != nil {
		t.Fatalf("accept-new should learn an unknown host: %v", err)
	}
	if data, _ := os.ReadFile(path); len(data) == 0 {
		t.Fatal("expected the learned host to be appended to known_hosts")
	}

	// A fresh verifier loads the learned host and accepts the same key.
	cb2, err := knownHostsVerifier(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := cb2("10.0.0.2:22", remote, key); err != nil {
		t.Fatalf("known host with unchanged key should pass: %v", err)
	}
	// A CHANGED key for a known host must be rejected even under accept-new.
	if err := cb2("10.0.0.2:22", remote, testHostKey(t)); err == nil {
		t.Fatal("a changed host key must be rejected under accept-new")
	}
}

// TestKnownHostsVerifier_AcceptNew_ConcurrentSameHost locks down the
// TOCTOU fix: when many first-time handshakes to the SAME host race with
// DIFFERENT keys, exactly one key must be trusted and the rest rejected.
func TestKnownHostsVerifier_AcceptNew_ConcurrentSameHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	old := knownHostsFile
	knownHostsFile = path
	t.Cleanup(func() { knownHostsFile = old })

	cb, err := knownHostsVerifier(true)
	if err != nil {
		t.Fatal(err)
	}

	const n = 8
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.9"), Port: 22}

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < n; i++ {
		key := testHostKey(t) // distinct key per goroutine
		wg.Add(1)
		go func(k ssh.PublicKey) {
			defer wg.Done()
			if cb("10.0.0.9:22", remote, k) == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(key)
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("expected exactly 1 trusted key for the same host under a race, got %d", success)
	}
}

func TestKnownHostsVerifier_Strict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	old := knownHostsFile
	knownHostsFile = path
	t.Cleanup(func() { knownHostsFile = old })

	// Empty (but present) known_hosts: strict trusts nothing yet.
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	cb, err := knownHostsVerifier(false)
	if err != nil {
		t.Fatal(err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("10.0.0.3"), Port: 22}
	if err := cb("10.0.0.3:22", remote, testHostKey(t)); err == nil {
		t.Fatal("strict must reject a host that is not in known_hosts")
	}
}
