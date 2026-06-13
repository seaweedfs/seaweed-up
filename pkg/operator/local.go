package operator

import (
	"io"
	"os"
	"os/exec"
	"strconv"
)

type LocalOperator struct {
	// stdout/stderr redirect command output when non-nil (set via
	// SetOutput); nil falls back to os.Stdout/os.Stderr.
	stdout io.Writer
	stderr io.Writer
}

func NewLocalOperator() *LocalOperator {
	return &LocalOperator{}
}

// SetOutput implements OutputSink: command output is streamed to the given
// writers instead of os.Stdout/os.Stderr. Passing nil resets to os.Std*.
func (e *LocalOperator) SetOutput(stdout, stderr io.Writer) {
	e.stdout = stdout
	e.stderr = stderr
}

func (e *LocalOperator) Output(command string) ([]byte, error) {
	//TODO implement me
	panic("implement me")
}

// Execute runs command through bash, streaming stdout/stderr live to the
// configured sink (default os.Stdout/os.Stderr). Replaces go-execute's
// StreamStdio, which hardcodes os.Stdout and so can't be redirected into the
// live progress console.
func (e *LocalOperator) Execute(command string) error {
	cmd := exec.Command("/bin/bash", "-c", command)
	if e.stdout != nil {
		cmd.Stdout = e.stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if e.stderr != nil {
		cmd.Stderr = e.stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (e *LocalOperator) UploadFile(path string, remotePath string, mode string) error {
	source, err := os.Open(expandPath(path))
	if err != nil {
		return err
	}
	defer source.Close()

	return e.Upload(source, remotePath, mode)
}

func (e *LocalOperator) Upload(source io.Reader, remotePath string, mode string) error {
	permissions, err := strconv.ParseInt(mode, 8, 32)
	if err != nil {
		return err
	}

	destination, err := os.OpenFile(remotePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.FileMode(permissions))
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)

	return err
}
