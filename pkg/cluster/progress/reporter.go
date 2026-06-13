// Package progress renders a docker-compose-style live console for cluster
// operations: one in-place-updating line per component instance (master0,
// volume3, …) showing a spinner, state, the latest action, and elapsed time.
//
// It has two implementations behind the Reporter interface:
//   - live  — an ANSI renderer that repaints fixed lines in place (TTY only).
//   - plain — line-by-line [INFO]/[ERROR] logging identical to the old output
//     (used when stdout is not a TTY, when --plain is passed, and in tests).
//
// Construct one with New. A Reporter is safe for concurrent use: deploy fans
// component work out across many goroutines, each owning its own Task.
package progress

import (
	"io"
	"sync"
	"time"
)

// State is the lifecycle stage of a single component Task.
type State int

const (
	StatePending State = iota // queued, not started
	StateRunning              // in progress (animated spinner)
	StateDone                 // finished successfully
	StateFailed               // finished with an error
	StateSkipped              // not selected for this run (e.g. --component filter)
)

// Reporter drives the console. One Task per component instance.
type Reporter interface {
	// AddTask registers a component line shown as Pending until started.
	// id is a stable key ("volume3"); label is the display text
	// ("volume3 10.0.0.4:8080"). Call before Start (or any time; live
	// appends to the block).
	AddTask(id, label string) *Task
	// Log prints a line above the live block — non-component messages such
	// as "Starting rolling upgrade …". Plain mode prints "[INFO] <msg>".
	Log(msg string)
	// LogError prints an error line above the block. Plain: "[ERROR] <err>".
	LogError(err error)
	// Start begins rendering (live: launches the repaint goroutine).
	Start()
	// Stop flushes the final frame and releases the terminal. Idempotent.
	Stop()
	// Live reports whether this reporter renders an in-place block. Callers
	// use it to suppress verbose per-command tracing (which would scroll
	// above the block) that the live detail line already conveys.
	Live() bool
}

// reporterHooks is the private callback surface a Task uses to notify its
// owning Reporter of changes. Live ignores most (its ticker repaints); plain
// turns them into log lines.
type reporterHooks interface {
	taskDetail(t *Task, msg string) // detail already stored; decide to print
	taskStateChanged(t *Task)       // state already stored
	streamWriter(t *Task) io.Writer // sink for streamed command output
	clock() time.Time
}

// Task is a handle to one component instance's line. Its mutating methods are
// safe to call from the single goroutine that owns the task; the renderer
// reads a snapshot under the same mutex.
type Task struct {
	id    string
	label string
	rep   reporterHooks

	mu     sync.Mutex
	state  State
	detail string
	start  time.Time
	end    time.Time
	tail   *ring // bounded recent output, dumped on failure (live only)
}

// Start marks the task running and stamps its start time.
func (t *Task) Start() {
	t.mu.Lock()
	t.state = StateRunning
	if t.start.IsZero() {
		t.start = t.rep.clock()
	}
	t.mu.Unlock()
	t.rep.taskStateChanged(t)
}

// Detail sets the short current-action string shown on the task's line.
func (t *Task) Detail(msg string) {
	t.mu.Lock()
	t.detail = msg
	t.mu.Unlock()
	t.rep.taskDetail(t, msg)
}

// SetState changes the task state without other side effects.
func (t *Task) SetState(s State) {
	t.mu.Lock()
	t.state = s
	if s == StateRunning && t.start.IsZero() {
		t.start = t.rep.clock()
	}
	if (s == StateDone || s == StateFailed) && t.end.IsZero() {
		t.end = t.rep.clock()
	}
	t.mu.Unlock()
	t.rep.taskStateChanged(t)
}

// Done marks the task finished successfully.
func (t *Task) Done() {
	t.mu.Lock()
	t.state = StateDone
	t.end = t.rep.clock()
	t.mu.Unlock()
	t.rep.taskStateChanged(t)
}

// Fail marks the task failed and records the error as its detail.
func (t *Task) Fail(err error) {
	t.mu.Lock()
	t.state = StateFailed
	t.end = t.rep.clock()
	if err != nil {
		t.detail = err.Error()
	}
	t.mu.Unlock()
	t.rep.taskStateChanged(t)
}

// Writer returns an io.Writer sink for streamed command output. In live mode
// it splits lines, surfaces the last non-empty one as the task's detail, and
// keeps a bounded tail for the on-failure dump; in plain mode it passes
// through to the console so output scrolls as before.
func (t *Task) Writer() io.Writer {
	return t.rep.streamWriter(t)
}

// snapshot copies the fields the renderer needs under the task lock.
func (t *Task) snapshot() (state State, detail string, start, end time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state, t.detail, t.start, t.end
}

// tailLines returns a copy of the recent-output buffer (nil if none).
func (t *Task) tailLines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tail == nil {
		return nil
	}
	return t.tail.lines()
}
