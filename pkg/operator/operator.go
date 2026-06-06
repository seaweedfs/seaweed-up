package operator

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"

	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

type CommandRes struct {
	StdOut []byte
	StdErr []byte
}

type CommandOperator interface {
	Execute(command string) error
	Output(command string) ([]byte, error)
	Upload(src io.Reader, remotePath string, mode string) error
	UploadFile(path string, remotePath string, mode string) error
}

type Callback func(CommandOperator) error

// BastionConfig describes an SSH jump host. When one is installed via
// SetBastion, every ExecuteRemote connection is dialed *through* the
// bastion instead of connecting to the target directly — the standard
// "ssh bastion, then ssh 10.0.0.x" topology for nodes that only have
// private addresses.
type BastionConfig struct {
	Host     string // bastion address, "host" or "host:port"
	Port     int    // ssh port; 0 means 22 (ignored if Host carries a port)
	User     string // ssh user; empty means the current OS user
	Identity string // private key path; empty falls back to the ssh agent
	Password string // optional password auth
}

// Jump-host state. defaultBastion, when non-nil, routes all
// ExecuteRemote connections through the configured jump host. It is set
// once at command start (from global.bastion in cluster.yaml, via
// SetBastion) before any concurrent fan-out and only read afterwards.
//
// bastionConn caches a SINGLE SSH connection to the jump host that every
// target tunnel is multiplexed over. Without this, a concurrent fan-out
// (the deploy errgroup opens one connection per host) would open dozens
// of simultaneous SSH handshakes to the bastion and trip its sshd
// MaxStartups limit ("connection reset by peer"). One shared connection,
// many channels, sidesteps that entirely. *ssh.Client is safe for
// concurrent Dial/NewSession, so workers share it without further
// locking once it is established.
var (
	defaultBastion *BastionConfig

	bastionMu      sync.Mutex
	bastionConn    *ssh.Client
	bastionCleanup func() error
)

// SetBastion installs the process-wide jump host. Passing nil, or a
// config with an empty Host, clears it (direct connections). Any cached
// bastion connection is torn down so the next connection re-dials with
// the new config.
func SetBastion(b *BastionConfig) {
	if b != nil && b.Host == "" {
		b = nil
	}
	bastionMu.Lock()
	defer bastionMu.Unlock()
	closeBastionLocked()
	defaultBastion = b
}

// currentBastion returns the installed jump host (nil for direct
// connections), reading defaultBastion under the lock so it is safe to
// call from the concurrent deploy fan-out.
func currentBastion() *BastionConfig {
	bastionMu.Lock()
	defer bastionMu.Unlock()
	return defaultBastion
}

// sharedBastionClient returns the process-wide bastion connection,
// dialing it on first use. Concurrent callers serialize on the first
// dial and then all receive the same cached client.
func sharedBastionClient(b *BastionConfig) (*ssh.Client, error) {
	bastionMu.Lock()
	defer bastionMu.Unlock()

	if bastionConn != nil {
		return bastionConn, nil
	}

	addr := bastionAddress(b)
	user := b.User
	if user == "" {
		user = currentUserName()
	}
	method, cleanup, err := resolveAuthMethod(b.Identity, b.Password)
	if err != nil {
		return nil, fmt.Errorf("bastion %s auth: %w", addr, err)
	}

	conn, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{method},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		_ = cleanup()
		return nil, fmt.Errorf("connect to bastion %s: %w", addr, err)
	}

	bastionConn = conn
	bastionCleanup = cleanup
	return bastionConn, nil
}

// dropBastion discards the cached bastion connection if it is still the
// one the caller saw, so a later call re-dials. Used when a tunnel dial
// fails, which usually means the shared connection has dropped.
func dropBastion(stale *ssh.Client) {
	bastionMu.Lock()
	defer bastionMu.Unlock()
	if bastionConn == stale {
		closeBastionLocked()
	}
}

// closeBastionLocked tears down the cached bastion connection. Caller
// must hold bastionMu.
func closeBastionLocked() {
	if bastionConn != nil {
		_ = bastionConn.Close()
		bastionConn = nil
	}
	if bastionCleanup != nil {
		_ = bastionCleanup()
		bastionCleanup = nil
	}
}

func ExecuteLocal(callback Callback) error {
	return callback(NewLocalOperator())
}

func ExecuteRemote(host string, user string, privateKey string, password string, callback Callback) error {
	method, cleanup, err := resolveAuthMethod(privateKey, password)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()

	return executeRemote(host, user, method, callback)
}

// resolveAuthMethod builds an ssh.AuthMethod from a password, a private
// key file, or — when both are empty — the ssh agent. The returned
// cleanup closes any ssh-agent socket opened while resolving the method;
// it is never nil and is always safe to call.
func resolveAuthMethod(privateKey, password string) (ssh.AuthMethod, func() error, error) {
	noop := func() error { return nil }

	if password != "" {
		return ssh.Password(password), noop, nil
	}

	if privateKey == "" {
		// #nosec G704
		sshAgentConn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
		if err != nil {
			return nil, noop, NewSshAgentError(err)
		}

		client := agent.NewClient(sshAgentConn)
		list, err := client.List()
		if err != nil || len(list) == 0 {
			_ = sshAgentConn.Close()
			return nil, noop, NewSshAgentError(err)
		}

		return ssh.PublicKeysCallback(client.Signers), sshAgentConn.Close, nil
	}

	buffer, err := os.ReadFile(expandPath(privateKey))
	if err != nil {
		return nil, noop, errors.Wrapf(err, "unable to parse private key: %s", privateKey)
	}

	key, err := ssh.ParsePrivateKey(buffer)
	if err != nil {
		if err.Error() != "ssh: this private key is passphrase protected" {
			return nil, noop, errors.Wrapf(err, "unable to parse private key: %s", privateKey)
		}

		sshAgent, closeAgent := privateKeyUsingSSHAgent(privateKey + ".pub")
		if sshAgent != nil {
			return sshAgent, closeAgent, nil
		}
		// No matching signer in the agent: close the socket it opened
		// now rather than holding it idle through the passphrase prompt
		// and handshake.
		_ = closeAgent()

		fmt.Printf("Enter passphrase for '%s': ", privateKey)
		STDIN := int(os.Stdin.Fd())
		bytePassword, _ := term.ReadPassword(STDIN)
		fmt.Println()

		key, err = ssh.ParsePrivateKeyWithPassphrase(buffer, bytePassword)
		if err != nil {
			return nil, noop, errors.Wrapf(err, "parse private key with passphrase failed: %s", privateKey)
		}
		return ssh.PublicKeys(key), noop, nil
	}

	return ssh.PublicKeys(key), noop, nil
}

func privateKeyUsingSSHAgent(publicKeyPath string) (ssh.AuthMethod, func() error) {
	// #nosec G704
	if sshAgentConn, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		sshAgent := agent.NewClient(sshAgentConn)

		signers, err := sshAgent.Signers()
		if err != nil || len(signers) == 0 {
			return nil, sshAgentConn.Close
		}

		pubkey, err := os.ReadFile(expandPath(publicKeyPath))
		if err != nil {
			return nil, sshAgentConn.Close
		}

		authkey, _, _, _, err := ssh.ParseAuthorizedKey(pubkey)
		if err != nil {
			return nil, sshAgentConn.Close
		}
		parsedkey := authkey.Marshal()

		for _, signer := range signers {
			if bytes.Equal(signer.PublicKey().Marshal(), parsedkey) {
				return ssh.PublicKeys(signer), sshAgentConn.Close
			}
		}
	}
	return nil, func() error { return nil }
}

func executeRemote(address string, user string, authMethod ssh.AuthMethod, callback Callback) error {

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") {
			host = address
			port = "22"
		} else {
			return fmt.Errorf("error splitting host/port: %w", err)
		}
	}
	targetAddr := net.JoinHostPort(host, port)

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			authMethod,
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	operator, err := dialOperator(targetAddr, config)

	if err != nil {
		return NewTargetConnectError(err)
	}

	defer operator.Close()

	return callback(operator)
}

// dialOperator connects to targetAddr, routing through the configured
// bastion when SetBastion has installed one, and connecting directly
// otherwise.
func dialOperator(targetAddr string, config *ssh.ClientConfig) (*SSHOperator, error) {
	b := currentBastion()
	if b == nil {
		return NewSSHOperator(targetAddr, config)
	}
	return newSSHOperatorViaBastion(b, targetAddr, config)
}

// bastionAddress returns the bastion's "host:port", honoring an explicit
// port carried in Host, then BastionConfig.Port, then defaulting to 22.
func bastionAddress(b *BastionConfig) string {
	if _, _, err := net.SplitHostPort(b.Host); err == nil {
		return b.Host
	}
	port := b.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(b.Host, strconv.Itoa(port))
}

// currentUserName returns the current OS user's login name, or "" if it
// cannot be determined.
func currentUserName() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func expandPath(path string) string {
	res, err := homedir.Expand(path)
	if err != nil {
		return path
	}
	return res
}
