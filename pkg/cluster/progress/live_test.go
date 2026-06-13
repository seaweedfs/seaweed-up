package progress

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// fixedNow returns a clock so elapsed times are deterministic.
func fixedNow() func() time.Time {
	tm := time.Unix(1000, 0)
	return func() time.Time { return tm }
}

func TestLiveFinalFrame(t *testing.T) {
	var buf bytes.Buffer
	r := newLiveReporterWith(&buf, 80, fixedNow())

	done := r.AddTask("volume0", "volume0 10.0.0.1:8080")
	failed := r.AddTask("master0", "master0 10.0.0.1:9333")
	r.AddTask("filer0", "filer0 10.0.0.1:8888") // stays Pending

	done.Start()
	done.Detail("installing")
	done.Done()

	failed.Start()
	fmt.Fprintln(failed.Writer(), "  -> Downloading weed")
	fmt.Fprintln(failed.Writer(), "curl: (22) 404 Not Found")
	failed.Fail(errors.New("install failed"))

	// pending stays Pending (never started)
	r.Stop() // not started → one synchronous final render

	out := stripANSI(buf.String())

	for _, want := range []string{
		"volume0 10.0.0.1:8080",
		"master0 10.0.0.1:9333",
		"filer0 10.0.0.1:8888",
		"✓", // done icon
		"✗", // failed icon
		"·", // pending icon
		"master0 10.0.0.1:9333 failed: install failed", // failure header
		"curl: (22) 404 Not Found",                     // dumped tail line
	} {
		if !strings.Contains(out, want) {
			t.Errorf("final frame missing %q\n--- frame ---\n%s", want, out)
		}
	}
}

func TestLiveTruncatesToWidth(t *testing.T) {
	var buf bytes.Buffer
	width := 40
	r := newLiveReporterWith(&buf, width, fixedNow())
	task := r.AddTask("volume0", "volume0")
	task.Start()
	task.Detail(strings.Repeat("x", 200)) // far wider than the terminal
	r.Stop()

	for _, line := range strings.Split(stripANSI(buf.String()), "\n") {
		if strings.Contains(line, "volume0") {
			if l := len([]rune(line)); l > width {
				t.Errorf("line exceeds width %d: %d runes\n%q", width, l, line)
			}
			if !strings.Contains(line, "…") {
				t.Errorf("over-long line should be truncated with an ellipsis: %q", line)
			}
		}
	}
}

func TestLiveStartStopNoDeadlock(t *testing.T) {
	var buf bytes.Buffer
	r := newLiveReporterWith(&buf, 80, fixedNow())
	r.interval = time.Millisecond // tick fast so the loop runs a few frames
	task := r.AddTask("volume0", "volume0")
	r.Start()
	task.Start()
	time.Sleep(5 * time.Millisecond)
	task.Done()
	r.Stop()
	r.Stop() // idempotent

	if !strings.Contains(stripANSI(buf.String()), "volume0") {
		t.Error("expected the task to be rendered")
	}
}

func TestLiveLogAppearsAboveBlock(t *testing.T) {
	var buf bytes.Buffer
	r := newLiveReporterWith(&buf, 80, fixedNow())
	r.AddTask("volume0", "volume0")
	r.Log("Resolved dev build 20260613")
	r.Stop()
	if !strings.Contains(stripANSI(buf.String()), "Resolved dev build 20260613") {
		t.Error("Log line should be flushed into the rendered output")
	}
}
