package progress

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// plainReporter reproduces the historical line-by-line output: component
// messages and the streamed command output scroll as [INFO]/[ERROR] lines and
// raw passthrough, with no in-place updating. Used for non-TTY stdout, the
// --plain flag, and tests, so CI logs and log-scraping stay unchanged.
type plainReporter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewPlain returns a plain reporter writing to w. The default reporter on a
// freshly-constructed Manager, which is what keeps non-TTY tests green.
func NewPlain(w io.Writer) Reporter {
	return &plainReporter{w: w}
}

func (r *plainReporter) AddTask(id, label string) *Task {
	return &Task{id: id, label: label, rep: r, state: StatePending}
}

func (r *plainReporter) Log(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = fmt.Fprintln(r.w, "[INFO]", msg)
}

func (r *plainReporter) LogError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = fmt.Fprintf(r.w, "[ERROR] %v\n", err)
}

func (r *plainReporter) Start()     {}
func (r *plainReporter) Stop()      {}
func (r *plainReporter) Live() bool { return false }

// taskDetail prints the action as an [INFO] line — the old info("Deploying
// volume3…") behavior, preserved.
func (r *plainReporter) taskDetail(t *Task, msg string) {
	r.Log(msg)
}

// taskStateChanged is silent except on failure: today there is no per-state
// line, and a failure prints a single [ERROR]. (Callers must not also LogError
// the same error, to avoid duplicates.)
func (r *plainReporter) taskStateChanged(t *Task) {
	state, detail, _, _ := t.snapshot()
	if state == StateFailed {
		r.mu.Lock()
		defer r.mu.Unlock()
		if detail != "" {
			_, _ = fmt.Fprintf(r.w, "[ERROR] %s\n", detail)
		} else {
			_, _ = fmt.Fprintf(r.w, "[ERROR] %s failed\n", t.label)
		}
	}
}

// streamWriter passes streamed command output straight through (mutex-guarded
// so concurrent component streams don't tear mid-write), matching the old
// raw-to-stdout behavior.
func (r *plainReporter) streamWriter(t *Task) io.Writer {
	return &lockedWriter{mu: &r.mu, w: r.w}
}

func (r *plainReporter) clock() time.Time { return time.Now() }

// lockedWriter serializes writes to an underlying writer.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
