package plan

import (
	"bytes"
	"fmt"
	"strings"
)

// UnifiedDiff renders a minimal unified-style diff between oldText and
// newText, with `name` used as the file label on the `---` / `+++`
// header lines. Returns an empty string when the inputs are
// byte-identical (no diff needed).
//
// Output style:
//
//	--- <name> (current)
//	+++ <name> (proposed)
//	@@ -<start>,<count> +<start>,<count> @@
//	 unchanged line
//	-removed line
//	+added line
//
// The renderer is hand-rolled (line-based LCS, no external diff
// library) because `cluster plan --dry-run`'s only consumer is the
// human reading stderr — perfect alignment with `git diff` formatting
// is not a goal. The format is close enough that operators recognize
// it and `diff`-aware tooling (less +F, IDE diff viewers) renders it
// usefully.
//
// Context lines around each hunk are fixed at three (matching `diff
// -u`'s default). For greenfield runs (oldText empty) the entire
// new file shows as one large hunk of `+` lines; for an append-merge
// no-op the function returns "" without printing any header.
func UnifiedDiff(name string, oldText, newText []byte) string {
	if bytes.Equal(oldText, newText) {
		return ""
	}
	// When BOTH inputs end with '\n' (the cluster.yaml norm) we
	// drop the trailing empty element so the diff is free of
	// phantom blank-line context. An empty input is treated as
	// "compatible with either" — greenfield diffs (oldText nil)
	// against a normal newline-terminated newText then produce the
	// expected `+1,N` hunk instead of one with a trailing blank
	// addition. When exactly one non-empty side ends without '\n',
	// the trailing "" stays so the LCS can surface the difference
	// (otherwise "a\n" vs "a" would reduce to identical line lists
	// and emit a header with no hunks).
	oldEndsNL := len(oldText) == 0 || bytes.HasSuffix(oldText, []byte{'\n'})
	newEndsNL := len(newText) == 0 || bytes.HasSuffix(newText, []byte{'\n'})
	dropTrailing := oldEndsNL && newEndsNL
	oldLines := splitLines(oldText, dropTrailing)
	newLines := splitLines(newText, dropTrailing)

	// Compute the LCS table on lines. The cluster.yaml files we feed
	// this are O(100s) of lines, so the O(n*m) table is fine. The
	// table is laid out as one flat slice (cols-stride) for cache
	// locality and a single allocation; lcsAt does the indexing.
	lcs, cols := lcsTable(oldLines, newLines)
	ops := diffOps(oldLines, newLines, lcs, cols)
	hunks := groupHunks(ops, 3)

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (current)\n", name)
	fmt.Fprintf(&b, "+++ %s (proposed)\n", name)
	for _, h := range hunks {
		writeHunk(&b, h)
	}
	return b.String()
}

// splitLines breaks raw into lines via strings.Split. When
// dropTrailingEmpty is true and the input ends in '\n', the empty
// trailing element strings.Split returns is dropped so the diff
// doesn't render a phantom blank line of context. Caller (UnifiedDiff)
// only sets that flag when BOTH inputs end in '\n'; if exactly one
// does, the trailing "" stays so the LCS can surface the
// no-trailing-newline difference instead of silently flattening
// "a\n" and "a" to the same line list.
func splitLines(raw []byte, dropTrailingEmpty bool) []string {
	if len(raw) == 0 {
		return nil
	}
	parts := strings.Split(string(raw), "\n")
	if dropTrailingEmpty && len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// lcsTable returns the standard dynamic-programming length-of-LCS
// table for a and b, laid out as a single flat slice with `cols`
// stride: the cell for (i, j) is at table[i*cols + j]. Length at
// (i, j) is the LCS of a[i:] and b[j:]; indexing from the end keeps
// diffOps's recovery loop tidy. One contiguous allocation, instead
// of the previous slice-of-slices that allocated len(a)+1 separate
// row backing arrays.
func lcsTable(a, b []string) (table []int, cols int) {
	rows := len(a) + 1
	cols = len(b) + 1
	table = make([]int, rows*cols)
	for i := len(a) - 1; i >= 0; i-- {
		row := i * cols
		next := row + cols
		for j := len(b) - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				table[row+j] = table[next+j+1] + 1
			case table[next+j] >= table[row+j+1]:
				table[row+j] = table[next+j]
			default:
				table[row+j] = table[row+j+1]
			}
		}
	}
	return table, cols
}

// diffOp is one line in the per-line edit script. Kind is ' ' (kept),
// '-' (removed from old), or '+' (added in new). OldLine / NewLine
// are 1-based positions used by the hunk header; one of them is 0
// when the line only exists on one side.
type diffOp struct {
	kind     byte
	text     string
	oldLine  int
	newLine  int
}

// diffOps walks the LCS table and emits the flat edit script. Output
// lines are in interleaved order: removals come before additions at
// the same divergence point so the hunk reads `-old / +new`. The
// table is the flat layout from lcsTable; (i, j) lives at
// lcs[i*cols + j].
func diffOps(a, b []string, lcs []int, cols int) []diffOp {
	var ops []diffOp
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{kind: ' ', text: a[i], oldLine: i + 1, newLine: j + 1})
			i++
			j++
		case lcs[(i+1)*cols+j] >= lcs[i*cols+(j+1)]:
			ops = append(ops, diffOp{kind: '-', text: a[i], oldLine: i + 1})
			i++
		default:
			ops = append(ops, diffOp{kind: '+', text: b[j], newLine: j + 1})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{kind: '-', text: a[i], oldLine: i + 1})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{kind: '+', text: b[j], newLine: j + 1})
	}
	return ops
}

// hunk is a contiguous slice of ops surrounded by up to `context`
// lines of unchanged context on each side.
type hunk struct {
	oldStart, oldCount int
	newStart, newCount int
	ops                []diffOp
}

// groupHunks slices the flat op stream into the per-hunk groups a
// unified diff renders. Each hunk wraps a run of changes (`-`/`+`)
// in up to `context` unchanged lines on each side; runs of context
// longer than 2*context collapse into a hunk boundary.
func groupHunks(ops []diffOp, context int) []hunk {
	if len(ops) == 0 {
		return nil
	}
	// First, find the indices of changed ops.
	var changes []int
	for i, o := range ops {
		if o.kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return nil
	}

	var hunks []hunk
	start := 0
	end := 0
	first := true
	for i, idx := range changes {
		if first {
			start = idx - context
			if start < 0 {
				start = 0
			}
			end = idx + context + 1
			first = false
			continue
		}
		// If this change is within 2*context of the previous hunk's
		// trailing context, keep extending; otherwise flush.
		if idx-changes[i-1] <= 2*context {
			end = idx + context + 1
		} else {
			hunks = append(hunks, makeHunk(ops, start, end))
			start = idx - context
			if start < 0 {
				start = 0
			}
			end = idx + context + 1
		}
	}
	if end > len(ops) {
		end = len(ops)
	}
	hunks = append(hunks, makeHunk(ops, start, end))
	return hunks
}

// makeHunk slices ops[start:end] and computes the hunk's old/new
// header coordinates from the first op that bears each.
func makeHunk(ops []diffOp, start, end int) hunk {
	if end > len(ops) {
		end = len(ops)
	}
	if start < 0 {
		start = 0
	}
	h := hunk{ops: ops[start:end]}
	for _, o := range h.ops {
		if o.kind == ' ' || o.kind == '-' {
			if h.oldStart == 0 {
				h.oldStart = o.oldLine
			}
			h.oldCount++
		}
		if o.kind == ' ' || o.kind == '+' {
			if h.newStart == 0 {
				h.newStart = o.newLine
			}
			h.newCount++
		}
	}
	// Empty-side hunks (e.g. pure-additions when oldText is empty)
	// keep the zero-initialized 0,0 header from the struct literal —
	// the loop above only ever writes non-zero starts via the
	// `h.oldStart == 0` / `h.newStart == 0` guards.
	return h
}

func writeHunk(b *strings.Builder, h hunk) {
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", h.oldStart, h.oldCount, h.newStart, h.newCount)
	for _, o := range h.ops {
		b.WriteByte(o.kind)
		b.WriteString(o.text)
		b.WriteByte('\n')
	}
}
