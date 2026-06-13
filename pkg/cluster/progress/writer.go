package progress

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// ring is a fixed-capacity FIFO of the most recent output lines, used to dump
// a component's tail when it fails (its scrolling output was hidden by the
// live block).
type ring struct {
	buf  []string
	max  int
	next int
	full bool
}

func newRing(max int) *ring {
	if max <= 0 {
		max = 20
	}
	return &ring{buf: make([]string, 0, max), max: max}
}

func (r *ring) push(line string) {
	if len(r.buf) < r.max {
		r.buf = append(r.buf, line)
		return
	}
	r.buf[r.next] = line
	r.next = (r.next + 1) % r.max
	r.full = true
}

// lines returns the buffered lines in chronological order.
func (r *ring) lines() []string {
	if !r.full {
		out := make([]string, len(r.buf))
		copy(out, r.buf)
		return out
	}
	out := make([]string, 0, r.max)
	out = append(out, r.buf[r.next:]...)
	out = append(out, r.buf[:r.next]...)
	return out
}

// logWriter turns streamed bytes into Reporter.Log lines. It is the default
// sink during a live render for operators that aren't bound to a specific
// task (host prep, security.toml, monitoring): their output scrolls above the
// block as ordinary log lines instead of corrupting the in-place display.
type logWriter struct {
	rep Reporter
	mu  sync.Mutex
	buf []byte
}

// NewLogWriter returns an io.Writer that emits each complete line it receives
// as a Reporter.Log message.
func NewLogWriter(rep Reporter) io.Writer { return &logWriter{rep: rep} }

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf = append(w.buf, p...)
	var lines []string
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	w.mu.Unlock()
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			w.rep.Log(line)
		}
	}
	return len(p), nil
}

// taskWriter is the live-mode sink for streamed command output. It buffers
// partial writes, and on every complete line: trims a trailing '\r', sets the
// task's detail to the last non-empty line, and appends to the task's tail
// ring. Width-agnostic — truncation happens at paint time.
type taskWriter struct {
	task *Task
	mu   sync.Mutex
	buf  []byte
}

func newTaskWriter(t *Task) *taskWriter {
	t.mu.Lock()
	if t.tail == nil {
		t.tail = newRing(20)
	}
	t.mu.Unlock()
	return &taskWriter{task: t}
}

func (w *taskWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf = append(w.buf, p...)
	var lines []string
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		lines = append(lines, line)
	}
	w.mu.Unlock()

	for _, line := range lines {
		w.task.mu.Lock()
		if w.task.tail != nil {
			w.task.tail.push(line)
		}
		w.task.mu.Unlock()
		if s := strings.TrimSpace(line); s != "" {
			w.task.Detail(s) // surfaces as the live line's detail
		}
	}
	return len(p), nil
}
