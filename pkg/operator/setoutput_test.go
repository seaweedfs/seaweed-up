package operator

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// LocalOperator must satisfy the optional OutputSink interface so the live
// progress console can redirect its streamed output.
func TestLocalOperatorImplementsOutputSink(t *testing.T) {
	var op CommandOperator = NewLocalOperator()
	if _, ok := op.(OutputSink); !ok {
		t.Fatal("NewLocalOperator() should implement OutputSink")
	}
}

// A bare fake that only implements CommandOperator must NOT be an OutputSink,
// documenting the optional-interface contract that keeps test fakes working.
type bareOp struct{}

func (bareOp) Execute(string) error                    { return nil }
func (bareOp) Output(string) ([]byte, error)           { return nil, nil }
func (bareOp) Upload(io.Reader, string, string) error  { return nil }
func (bareOp) UploadFile(string, string, string) error { return nil }

func TestBareFakeIsNotOutputSink(t *testing.T) {
	var op CommandOperator = bareOp{}
	if _, ok := op.(OutputSink); ok {
		t.Fatal("a fake without SetOutput must not satisfy OutputSink")
	}
}

func TestLocalOperatorSetOutputRedirects(t *testing.T) {
	op := NewLocalOperator()
	var out, errb bytes.Buffer
	op.SetOutput(&out, &errb)

	if err := op.Execute("echo hi; echo oops 1>&2"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "hi" {
		t.Errorf("stdout: got %q, want %q", got, "hi")
	}
	if got := strings.TrimSpace(errb.String()); got != "oops" {
		t.Errorf("stderr: got %q, want %q", got, "oops")
	}
}

// Passing nil resets the sink back to the os.Std* defaults.
func TestLocalOperatorSetOutputNilResets(t *testing.T) {
	op := NewLocalOperator()
	op.SetOutput(&bytes.Buffer{}, &bytes.Buffer{})
	op.SetOutput(nil, nil)
	if op.stdout != nil || op.stderr != nil {
		t.Fatal("SetOutput(nil,nil) should clear the writers")
	}
	// Execute must still succeed writing to the default os.Stdout.
	if err := op.Execute("true"); err != nil {
		t.Fatalf("Execute after reset: %v", err)
	}
	_ = os.Stdout
}

// White-box: the SSH operator's outW/errW fall back to os.Std* when unset.
func TestSSHOperatorOutputFallback(t *testing.T) {
	s := &SSHOperator{}
	if s.outW() != os.Stdout {
		t.Error("outW() should default to os.Stdout")
	}
	if s.errW() != os.Stderr {
		t.Error("errW() should default to os.Stderr")
	}
	var buf bytes.Buffer
	s.SetOutput(&buf, &buf)
	if s.outW() != &buf || s.errW() != &buf {
		t.Error("SetOutput should redirect outW/errW")
	}
}
