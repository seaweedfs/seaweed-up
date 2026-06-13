package progress

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPlainReporterOutput(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlain(&buf)
	r.Start()

	r.Log("Starting rolling upgrade")
	r.LogError(errors.New("boom"))

	task := r.AddTask("volume0", "volume0 10.0.0.1:8080")
	task.Detail("Deploying volume0...") // -> [INFO] line
	task.Start()                        // silent
	fmt.Fprintln(task.Writer(), "  -> Downloading weed")
	task.Done() // silent

	r.Stop()
	out := buf.String()

	for _, want := range []string{
		"[INFO] Starting rolling upgrade\n",
		"[ERROR] boom\n",
		"[INFO] Deploying volume0...\n",
		"  -> Downloading weed\n", // streamed output passes through raw
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing %q\n--- got ---\n%s", want, out)
		}
	}
	// No ANSI escapes in plain mode.
	if strings.Contains(out, "\x1b[") {
		t.Errorf("plain output must not contain ANSI escapes:\n%q", out)
	}
}

func TestPlainReporterFailPrintsError(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlain(&buf)
	task := r.AddTask("master0", "master0 10.0.0.1:9333")
	task.Fail(errors.New("health check failed"))
	if got := buf.String(); !strings.Contains(got, "[ERROR] health check failed\n") {
		t.Errorf("Fail should print the error in plain mode, got %q", got)
	}
}

func TestPlainReporterStateTransitionsSilent(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlain(&buf)
	task := r.AddTask("filer0", "filer0")
	task.Start()
	task.SetState(StateRunning)
	task.Done()
	if got := buf.String(); got != "" {
		t.Errorf("Start/Running/Done should be silent in plain mode, got %q", got)
	}
}
