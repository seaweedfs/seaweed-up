package operator

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/bramvdbogaerde/go-scp"
	"golang.org/x/crypto/ssh"
)

type SSHOperator struct {
	conn *ssh.Client
	// cleanup, when set (bastion connections), tears down the whole
	// chain — target client, bastion client, and any ssh-agent socket.
	// When nil (direct connections) Close just closes conn.
	cleanup func() error
	// stdout/stderr redirect streamed command output when non-nil (set via
	// SetOutput); nil falls back to os.Stdout/os.Stderr. One operator is
	// owned by one goroutine, so these need no locking.
	stdout io.Writer
	stderr io.Writer
}

// SetOutput implements OutputSink: streamed Execute output (and Output's
// stderr) is redirected to the given writers. Passing nil resets to os.Std*.
func (s *SSHOperator) SetOutput(stdout, stderr io.Writer) {
	s.stdout = stdout
	s.stderr = stderr
}

func (s *SSHOperator) outW() io.Writer {
	if s.stdout != nil {
		return s.stdout
	}
	return os.Stdout
}

func (s *SSHOperator) errW() io.Writer {
	if s.stderr != nil {
		return s.stderr
	}
	return os.Stderr
}

func NewSSHOperator(address string, config *ssh.ClientConfig) (*SSHOperator, error) {
	conn, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return nil, err
	}

	operator := SSHOperator{
		conn: conn,
	}

	return &operator, nil
}

// newSSHOperatorViaBastion dials targetAddr through a jump host: it
// reuses the single shared SSH connection to the bastion, asks the
// bastion to open a TCP connection out to the target, then completes the
// SSH handshake with the target over that tunnel. The result is an
// ordinary *ssh.Client, so the rest of the operator works unchanged.
//
// Many target tunnels are multiplexed over the one bastion connection
// (see sharedBastionClient), so a concurrent fan-out does not flood the
// bastion's sshd. Close shuts down only this target connection; the
// shared bastion connection is left up for other tunnels and torn down
// by SetBastion. If the tunnel dial fails — typically because the shared
// connection has dropped — the bastion is re-dialed once.
func newSSHOperatorViaBastion(b *BastionConfig, targetAddr string, config *ssh.ClientConfig) (*SSHOperator, error) {
	bastionAddr := bastionAddress(b)

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		bastionClient, err := sharedBastionClient(b)
		if err != nil {
			return nil, err
		}

		conn, err := bastionClient.Dial("tcp", targetAddr)
		if err != nil {
			// The shared connection is likely dead; drop it so the
			// next attempt re-dials the bastion from scratch.
			dropBastion(bastionClient)
			lastErr = fmt.Errorf("dial %s through bastion %s: %w", targetAddr, bastionAddr, err)
			continue
		}

		ncc, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, config)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ssh handshake with %s through bastion %s: %w", targetAddr, bastionAddr, err)
		}

		client := ssh.NewClient(ncc, chans, reqs)
		return &SSHOperator{
			conn:    client,
			cleanup: client.Close,
		}, nil
	}
	return nil, lastErr
}

func (s *SSHOperator) Close() error {
	if s.cleanup != nil {
		return s.cleanup()
	}
	return s.conn.Close()
}

func (s *SSHOperator) Output(command string) (output []byte, err error) {
	sess, err := s.conn.NewSession()
	if err != nil {
		return nil, err
	}

	defer sess.Close()

	// Only stderr is redirected; stdout is returned to the caller (upgrade
	// health/version probes parse it), so it must not go to the sink.
	sess.Stderr = s.errW()
	output, err = sess.Output(command)

	return output, err
}

func (s *SSHOperator) Execute(command string) error {
	sess, err := s.conn.NewSession()
	if err != nil {
		return err
	}

	defer sess.Close()

	sess.Stdout = s.outW()
	sess.Stderr = s.errW()
	err = sess.Run(command)

	return err
}

func (s *SSHOperator) Upload(source io.Reader, remotePath string, mode string) error {
	client, _ := scp.NewClientBySSH(s.conn)
	return client.CopyFile(context.Background(), source, remotePath, mode)
}

func (s *SSHOperator) UploadFile(path string, remotePath string, mode string) error {
	source, err := os.Open(expandPath(path))
	if err != nil {
		return err
	}
	defer source.Close()

	return s.Upload(source, remotePath, mode)
}
