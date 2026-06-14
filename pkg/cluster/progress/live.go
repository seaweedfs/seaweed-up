package progress

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fatih/color"
)

// liveReporter repaints a fixed block of one line per Task in place using ANSI
// cursor control. A single goroutine owns the terminal: worker goroutines only
// mutate their own Task (under its lock); the repaint loop reads snapshots.
type liveReporter struct {
	w        io.Writer
	interval time.Duration
	widthFn  func() int
	nowFn    func() time.Time
	utf8     bool

	mu        sync.Mutex
	tasks     []*Task
	pending   []string // log lines / failure dumps to print above the block
	prevLines int
	frame     int
	started   bool
	ticker    *time.Ticker
	done      chan struct{}
	loopDone  chan struct{}
	stopOnce  sync.Once
}

func newLiveReporter(w io.Writer) *liveReporter {
	return newLiveReporterWith(w, defaultWidth, time.Now)
}

// newLiveReporterWith is the test seam: a fixed terminal width and an
// injectable clock let the live path run deterministically under non-TTY
// `go test`, where it would otherwise never be selected.
func newLiveReporterWith(w io.Writer, width int, now func() time.Time) *liveReporter {
	return &liveReporter{
		w:        w,
		interval: 100 * time.Millisecond,
		widthFn:  func() int { return width },
		nowFn:    now,
		utf8:     true,
		done:     make(chan struct{}),
		loopDone: make(chan struct{}),
	}
}

func (r *liveReporter) AddTask(id, label string) *Task {
	t := &Task{id: id, label: label, rep: r, state: StatePending}
	r.mu.Lock()
	r.tasks = append(r.tasks, t)
	r.mu.Unlock()
	return t
}

func (r *liveReporter) Log(msg string)     { r.enqueue(msg) }
func (r *liveReporter) LogError(err error) { r.enqueue(color.New(color.FgRed).Sprintf("%v", err)) }

func (r *liveReporter) Live() bool { return true }

func (r *liveReporter) enqueue(line string) {
	r.mu.Lock()
	r.pending = append(r.pending, line)
	r.mu.Unlock()
}

func (r *liveReporter) Start() {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.ticker = time.NewTicker(r.interval)
	r.mu.Unlock()
	go r.loop()
}

func (r *liveReporter) loop() {
	defer close(r.loopDone)
	for {
		select {
		case <-r.done:
			r.mu.Lock()
			r.render(true)
			r.mu.Unlock()
			return
		case <-r.ticker.C:
			r.mu.Lock()
			r.frame++
			r.render(false)
			r.mu.Unlock()
		}
	}
}

func (r *liveReporter) Stop() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		started := r.started
		r.mu.Unlock()
		if !started {
			// Never started (no repaint goroutine): render once synchronously.
			r.mu.Lock()
			r.render(true)
			r.mu.Unlock()
			return
		}
		r.ticker.Stop()
		close(r.done)
		<-r.loopDone // the loop performs the final render, so no concurrent paint
	})
}

// --- reporterHooks ---

func (r *liveReporter) taskDetail(t *Task, msg string) {} // ticker repaints

// taskStateChanged dumps a failed task's captured output tail above the block,
// since its scrolling output was hidden by the live line.
func (r *liveReporter) taskStateChanged(t *Task) {
	state, detail, _, _ := t.snapshot()
	if state != StateFailed {
		return
	}
	lines := t.tailLines()
	r.mu.Lock()
	r.pending = append(r.pending, color.New(color.FgRed).Sprintf("%s %s failed: %s", r.failIcon(), t.label, detail))
	for _, ln := range lines {
		r.pending = append(r.pending, "    "+ln)
	}
	r.mu.Unlock()
}

func (r *liveReporter) streamWriter(t *Task) io.Writer { return newTaskWriter(t) }
func (r *liveReporter) clock() time.Time               { return r.nowFn() }

// --- rendering ---

// render paints one frame. Caller holds r.mu.
func (r *liveReporter) render(final bool) {
	var b bytes.Buffer
	if r.prevLines > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", r.prevLines) // cursor up to top of block
	}
	if r.prevLines > 0 || len(r.pending) > 0 {
		b.WriteString("\x1b[J") // clear from cursor to end of screen
	}
	for _, ln := range r.pending {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	r.pending = nil

	width := r.widthFn()
	if width <= 0 {
		width = defaultWidth
	}
	for _, t := range r.tasks {
		b.WriteString(r.renderLine(t, final, width))
		b.WriteByte('\n')
	}
	r.prevLines = len(r.tasks)
	_, _ = b.WriteTo(r.w) // writes the buffered bytes directly, no string copy
}

func (r *liveReporter) renderLine(t *Task, final bool, width int) string {
	state, detail, start, end := t.snapshot()
	icon := r.coloredIcon(state, final)
	elapsed := r.elapsed(state, start, end)

	tail := ""
	if elapsed != "" {
		tail = "  " + elapsed
	}
	content := t.label
	if detail != "" {
		content = t.label + "  " + detail
	}
	reserved := 2 // icon + following space
	avail := width - reserved - runeLen(tail)
	if avail < 10 {
		avail = 10
	}
	content = r.truncate(content, avail)
	pad := width - reserved - runeLen(content) - runeLen(tail)
	if pad < 0 {
		pad = 0
	}
	return icon + " " + content + strings.Repeat(" ", pad) + tail
}

func (r *liveReporter) elapsed(state State, start, end time.Time) string {
	if start.IsZero() {
		return ""
	}
	var d time.Duration
	if (state == StateDone || state == StateFailed) && !end.IsZero() {
		d = end.Sub(start)
	} else {
		d = r.nowFn().Sub(start)
	}
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

var (
	spinnerUTF   = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	spinnerASCII = []rune{'|', '/', '-', '\\'}
)

func (r *liveReporter) coloredIcon(state State, final bool) string {
	switch state {
	case StateDone:
		return color.New(color.FgGreen).Sprint(r.doneIcon())
	case StateFailed:
		return color.New(color.FgRed).Sprint(r.failIcon())
	case StateSkipped:
		return color.New(color.Faint).Sprint(r.skipIcon())
	case StateRunning:
		if final {
			return color.New(color.FgGreen).Sprint(r.doneIcon())
		}
		sp := spinnerASCII
		if r.utf8 {
			sp = spinnerUTF
		}
		return color.New(color.FgCyan).Sprint(string(sp[r.frame%len(sp)]))
	default:
		return color.New(color.Faint).Sprint(r.pendingIcon())
	}
}

func (r *liveReporter) doneIcon() string {
	if r.utf8 {
		return "✓"
	}
	return "+"
}
func (r *liveReporter) failIcon() string {
	if r.utf8 {
		return "✗"
	}
	return "x"
}
func (r *liveReporter) pendingIcon() string {
	if r.utf8 {
		return "·"
	}
	return "."
}
func (r *liveReporter) skipIcon() string { return "-" }

func (r *liveReporter) truncate(s string, max int) string {
	if runeLen(s) <= max {
		return s
	}
	ell := "…"
	if !r.utf8 {
		ell = ".."
	}
	keep := max - runeLen(ell)
	if keep < 0 {
		keep = 0
	}
	out := make([]rune, 0, keep)
	for i, rr := range s {
		_ = i
		if len(out) >= keep {
			break
		}
		out = append(out, rr)
	}
	return string(out) + ell
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

const defaultWidth = 100
