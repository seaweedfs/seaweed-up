package operator

import (
	"context"
	"github.com/bramvdbogaerde/go-scp"
	"io"
	"os"

	"golang.org/x/crypto/ssh"
)

type SSHOperator struct {
	conn *ssh.Client
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

func (s SSHOperator) Close() error {
	return s.conn.Close()
}

func (s SSHOperator) Output(command string) (output []byte, err error) {
	sess, err := s.conn.NewSession()
	if err != nil {
		return nil, err
	}

	defer sess.Close()

	sess.Stderr = os.Stderr
	output, err = sess.Output(command)

	return output, err
}

func (s SSHOperator) Execute(command string) error {
	sess, err := s.conn.NewSession()
	if err != nil {
		return err
	}

	defer sess.Close()

	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	err = sess.Run(command)

	return err
}

func (s SSHOperator) Upload(source io.Reader, remotePath string, mode string) error {
	client, _ := scp.NewClientBySSH(s.conn)
	return client.CopyFile(context.Background(), source, remotePath, mode)
}

func (s SSHOperator) UploadFile(path string, remotePath string, mode string) error {
	source, err := os.Open(expandPath(path))
	if err != nil {
		return err
	}
	defer source.Close()

	return s.Upload(source, remotePath, mode)
}
