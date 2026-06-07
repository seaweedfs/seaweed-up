package operator

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSH host-key verification policies. The historical — and default —
// behavior is "ignore": host keys are not checked at all. Operators who
// want a real SSH trust boundary (protection against a MITM on the path
// to the bastion or the nodes) can opt into knownhosts-backed
// verification via global.ssh_host_key_check in cluster.yaml.
const (
	HostKeyIgnore    = "ignore"     // no verification (default; backward compatible)
	HostKeyAcceptNew = "accept-new" // trust-on-first-use: learn new hosts, reject changed keys
	HostKeyStrict    = "strict"     // every host must already be in known_hosts
)

var (
	hostKeyMu     sync.Mutex
	hostKeyPolicy string

	// knownHostsFile is the file consulted for accept-new/strict. It is a
	// var (not a const) so tests can point it at a temp file.
	knownHostsFile = "~/.ssh/known_hosts"

	// knownHostsWriteMu serializes appends so the concurrent deploy
	// fan-out does not interleave writes when learning new hosts.
	knownHostsWriteMu sync.Mutex
)

// SetHostKeyPolicy installs the process-wide host-key verification
// policy. An empty string resets to the default ("ignore").
func SetHostKeyPolicy(policy string) {
	hostKeyMu.Lock()
	defer hostKeyMu.Unlock()
	hostKeyPolicy = policy
}

func currentHostKeyPolicy() string {
	hostKeyMu.Lock()
	defer hostKeyMu.Unlock()
	if hostKeyPolicy == "" {
		return HostKeyIgnore
	}
	return hostKeyPolicy
}

// ValidHostKeyPolicy reports whether p is a recognized policy. The empty
// string is valid and means the default ("ignore").
func ValidHostKeyPolicy(p string) bool {
	switch p {
	case "", HostKeyIgnore, HostKeyAcceptNew, HostKeyStrict:
		return true
	}
	return false
}

// hostKeyCallback builds the ssh.HostKeyCallback for the active policy.
// Both the direct dial and the bastion hop use it, so the policy applies
// to every connection seaweed-up opens.
func hostKeyCallback() (ssh.HostKeyCallback, error) {
	switch currentHostKeyPolicy() {
	case HostKeyIgnore:
		return ssh.InsecureIgnoreHostKey(), nil
	case HostKeyAcceptNew:
		return knownHostsVerifier(true)
	case HostKeyStrict:
		return knownHostsVerifier(false)
	default:
		return nil, fmt.Errorf("unknown ssh host key policy %q (want %s, %s, or %s)",
			currentHostKeyPolicy(), HostKeyIgnore, HostKeyAcceptNew, HostKeyStrict)
	}
}

// knownHostsVerifier returns a callback backed by the known_hosts file.
// When acceptNew is true an unknown host is learned (appended) and
// accepted, but a host whose key has CHANGED is still rejected. When
// false (strict) any host not already present is rejected.
func knownHostsVerifier(acceptNew bool) (ssh.HostKeyCallback, error) {
	path, err := homedir.Expand(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("resolve known_hosts path: %w", err)
	}

	// knownhosts.New fails on a missing file. Under accept-new, create
	// an empty one so a first-ever deploy can populate it. Under strict,
	// leave it: a missing file legitimately means "nothing trusted yet".
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) && acceptNew {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create ssh dir: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create known_hosts: %w", err)
		}
		_ = f.Close()
	}

	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %s: %w", path, err)
	}
	if !acceptNew {
		return verify, nil
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
		if err == nil {
			return nil
		}
		// A KeyError with an empty Want means the host is simply unknown
		// — learn it. A non-empty Want means the presented key differs
		// from a recorded one (possible MITM); accept-new must still
		// reject that.
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			return appendKnownHost(path, hostname, remote, key)
		}
		return err
	}, nil
}

// appendKnownHost records a newly-seen host key in the known_hosts file.
func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	knownHostsWriteMu.Lock()
	defer knownHostsWriteMu.Unlock()

	addrs := []string{knownhosts.Normalize(hostname)}
	// Record the remote IP too, but only when it is meaningful. Bastion
	// tunnels present a synthetic 0.0.0.0:0 remote (the channel has no
	// real TCP peer), which would just be noise in known_hosts.
	if usefulRemoteAddr(remote) {
		if ra := knownhosts.Normalize(remote.String()); ra != addrs[0] {
			addrs = append(addrs, ra)
		}
	}
	line := knownhosts.Line(addrs, key)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open known_hosts for append: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("append to known_hosts: %w", err)
	}
	return nil
}

// usefulRemoteAddr reports whether a net.Addr carries a real routable
// peer worth recording in known_hosts. Bastion tunnels hand back a
// synthetic 0.0.0.0:0 addr, which is filtered out.
func usefulRemoteAddr(remote net.Addr) bool {
	if remote == nil {
		return false
	}
	host, port, err := net.SplitHostPort(remote.String())
	if err != nil || port == "" || port == "0" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return false
	}
	return true
}
