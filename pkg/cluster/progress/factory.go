package progress

import (
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// New returns a live reporter when w is a real terminal and plain is false;
// otherwise a plain reporter. This is the single decision point: pipes, CI,
// and --plain all fall back to the historical line-by-line output.
func New(w *os.File, plain bool) Reporter {
	if plain || !term.IsTerminal(int(w.Fd())) {
		return NewPlain(w)
	}
	lr := newLiveReporter(w)
	lr.utf8 = detectUTF8()
	lr.widthFn = func() int { return terminalWidth(w) }
	lr.nowFn = time.Now
	return lr
}

// terminalWidth returns the terminal's column count, or defaultWidth if it
// can't be determined.
func terminalWidth(w *os.File) int {
	if cols, _, err := term.GetSize(int(w.Fd())); err == nil && cols > 0 {
		return cols
	}
	return defaultWidth
}

// detectUTF8 reports whether the locale looks UTF-8 capable, gating the
// braille spinner / unicode icons vs. an ASCII fallback.
func detectUTF8() bool {
	for _, v := range []string{os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG")} {
		if v != "" {
			return strings.Contains(strings.ToLower(v), "utf")
		}
	}
	return false
}
