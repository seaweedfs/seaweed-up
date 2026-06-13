package progress

import (
	"fmt"
	"testing"
	"time"
)

func newTestLive() *liveReporter {
	return newLiveReporterWith(nil, 80, func() time.Time { return time.Unix(1000, 0) })
}

func TestTaskWriterLastNonEmptyLine(t *testing.T) {
	r := newTestLive()
	task := r.AddTask("volume0", "volume0")
	w := task.Writer()

	// Partial write (no newline yet) → detail unchanged.
	fmt.Fprint(w, "down")
	if _, d, _, _ := task.snapshot(); d != "" {
		t.Errorf("partial line should not set detail, got %q", d)
	}
	// Complete the line plus a blank line; detail is the last NON-empty line.
	fmt.Fprint(w, "loading\n\n")
	if _, d, _, _ := task.snapshot(); d != "downloading" {
		t.Errorf("detail = %q, want %q", d, "downloading")
	}
	// CRLF is trimmed.
	fmt.Fprint(w, "verifying\r\n")
	if _, d, _, _ := task.snapshot(); d != "verifying" {
		t.Errorf("detail = %q, want %q (CRLF not trimmed?)", d, "verifying")
	}
}

func TestTaskWriterTailRingBufferCap(t *testing.T) {
	r := newTestLive()
	task := r.AddTask("volume0", "volume0")
	w := task.Writer()
	for i := 0; i < 50; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	lines := task.tailLines()
	if len(lines) != 20 {
		t.Fatalf("tail kept %d lines, want cap 20", len(lines))
	}
	// Oldest dropped, newest retained, in chronological order.
	if lines[0] != "line 30" {
		t.Errorf("tail[0] = %q, want %q", lines[0], "line 30")
	}
	if lines[19] != "line 49" {
		t.Errorf("tail[19] = %q, want %q", lines[19], "line 49")
	}
}
