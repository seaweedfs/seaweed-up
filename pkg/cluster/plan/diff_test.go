package plan

import (
	"strings"
	"testing"
)

func TestUnifiedDiff_identical(t *testing.T) {
	in := []byte("a\nb\nc\n")
	if got := UnifiedDiff("file", in, in); got != "" {
		t.Errorf("identical inputs should produce empty diff, got:\n%s", got)
	}
}

func TestUnifiedDiff_greenfield(t *testing.T) {
	// Greenfield: oldText is empty, newText is the whole file. Every
	// line should appear with a `+` prefix and the hunk header should
	// start at old=0,0.
	got := UnifiedDiff("cluster.yaml", nil, []byte("a\nb\nc\n"))
	wantHeader := "@@ -0,0 +1,3 @@\n"
	if !strings.Contains(got, wantHeader) {
		t.Errorf("missing greenfield hunk header %q in:\n%s", wantHeader, got)
	}
	for _, line := range []string{"+a", "+b", "+c"} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing %q in greenfield diff:\n%s", line, got)
		}
	}
	if strings.Contains(got, "\n-") {
		t.Errorf("greenfield diff shouldn't contain removals:\n%s", got)
	}
}

func TestUnifiedDiff_appendOnly(t *testing.T) {
	// Append-merge: old file is a prefix of the new file. The output
	// should contain no `-` lines and the appended block should appear
	// as a single hunk near the tail.
	oldB := []byte("a\nb\nc\n")
	newB := []byte("a\nb\nc\nd\ne\n")
	got := UnifiedDiff("cluster.yaml", oldB, newB)
	if strings.Contains(got, "\n-") {
		t.Errorf("append-only diff contains removals:\n%s", got)
	}
	if !strings.Contains(got, "+d\n") || !strings.Contains(got, "+e\n") {
		t.Errorf("append-only diff missing appended lines:\n%s", got)
	}
	// The single hunk should reference the new range (old=…,3 +new=…,5).
	if !strings.Contains(got, "+1,5") {
		t.Errorf("append-only diff missing '+1,5' range:\n%s", got)
	}
}

func TestUnifiedDiff_overwriteShowsBothSides(t *testing.T) {
	// Overwrite: a single line edited mid-file. Should show both - and
	// + on adjacent lines, with surrounding context.
	oldB := []byte("a\nb\nc\nd\ne\n")
	newB := []byte("a\nb\nCHANGED\nd\ne\n")
	got := UnifiedDiff("cluster.yaml", oldB, newB)
	if !strings.Contains(got, "-c\n") {
		t.Errorf("overwrite diff missing removed line:\n%s", got)
	}
	if !strings.Contains(got, "+CHANGED\n") {
		t.Errorf("overwrite diff missing added line:\n%s", got)
	}
	// Three lines of context above and below — file is short, so we
	// expect ALL surrounding lines to appear as " <line>" entries.
	for _, line := range []string{" a", " b", " d", " e"} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("overwrite diff missing context line %q:\n%s", line, got)
		}
	}
}

func TestUnifiedDiff_distantHunksSeparate(t *testing.T) {
	// Two changes far apart should land in two separate hunks rather
	// than one giant block of context.
	oldB := []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n")
	newB := []byte("a\nB\nc\nd\ne\nf\ng\nh\nI\nj\n")
	got := UnifiedDiff("file", oldB, newB)
	hunks := strings.Count(got, "@@ -")
	if hunks != 2 {
		t.Errorf("expected 2 hunks for distant changes, got %d:\n%s", hunks, got)
	}
}

// TestUnifiedDiff_trailingNewlineDifference is the regression test
// for the splitLines drop-trailing-empty bug. Two inputs whose only
// difference is the trailing newline used to produce only the diff
// header with zero hunks, which read as "no changes" while the
// bytes were obviously different. The diff must surface the
// difference somewhere — even a phantom-blank-line entry is
// sufficient signal that the two inputs aren't byte-identical.
func TestUnifiedDiff_trailingNewlineDifference(t *testing.T) {
	got := UnifiedDiff("file", []byte("a\n"), []byte("a"))
	if got == "" {
		t.Fatal("inputs differ on trailing newline; diff must not be empty")
	}
	if !strings.Contains(got, "@@ -") {
		t.Errorf("expected at least one hunk in trailing-newline diff:\n%s", got)
	}
}

func TestUnifiedDiff_nameAppearsInHeader(t *testing.T) {
	got := UnifiedDiff("cluster.yaml", []byte("a\n"), []byte("b\n"))
	if !strings.Contains(got, "--- cluster.yaml (current)\n") ||
		!strings.Contains(got, "+++ cluster.yaml (proposed)\n") {
		t.Errorf("missing name-bearing header lines:\n%s", got)
	}
}
