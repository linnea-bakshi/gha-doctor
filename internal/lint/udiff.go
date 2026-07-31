package lint

import (
	"fmt"
	"strings"
)

// UnifiedDiff renders a unified diff (like `git diff`) between a and b with
// ctx lines of context. It returns "" when the inputs are identical. The
// implementation trims the common prefix/suffix and runs an LCS over the
// middle, so the localized edits --fix produces diff quickly even in very
// large workflow files; a pathological middle (beyond ~4M cell DP) degrades
// to one whole-block replacement rather than a slow or wrong answer.
func UnifiedDiff(aLabel, bLabel, a, b string, ctx int) string {
	if a == b {
		return ""
	}
	al := splitDiffLines(a)
	bl := splitDiffLines(b)

	ops := diffOps(al, bl)
	// Belt and braces: content that literally contains noEOLSentinel could
	// make two different inputs compare line-equal. If a != b but no op is
	// a change, fall back to a whole-file replacement so the diff is never
	// silently empty or wrong.
	changed := false
	for _, op := range ops {
		if op.kind != ' ' {
			changed = true
			break
		}
	}
	if !changed {
		ops = ops[:0]
		for _, l := range al {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range bl {
			ops = append(ops, diffOp{'+', l})
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", aLabel, bLabel)

	// Group ops into hunks: any run of changes plus ctx context lines,
	// merging hunks whose context would touch.
	type hunk struct{ start, end int } // op index range [start, end)
	var hunks []hunk
	for i := 0; i < len(ops); i++ {
		if ops[i].kind == ' ' {
			continue
		}
		start := i
		end := i + 1
		for end < len(ops) {
			// Extend while the next change is within 2*ctx equal lines.
			j := end
			eq := 0
			for j < len(ops) && ops[j].kind == ' ' {
				eq++
				j++
			}
			if j < len(ops) && eq <= 2*ctx {
				end = j + 1
				continue
			}
			break
		}
		hunks = append(hunks, hunk{start, end})
		i = end - 1
	}

	aLine, bLine := 1, 1
	opAt := 0
	for _, h := range hunks {
		// Advance line counters over the equal gap before the hunk.
		for ; opAt < h.start; opAt++ {
			aLine++
			bLine++
		}
		lead := min(ctx, h.start)
		aStart := aLine - lead
		bStart := bLine - lead
		var body strings.Builder
		aCount, bCount := 0, 0
		emit := func(kind byte, text string) {
			noEOL := strings.HasSuffix(text, noEOLSentinel)
			body.WriteByte(kind)
			body.WriteString(strings.TrimSuffix(text, noEOLSentinel))
			body.WriteByte('\n')
			if noEOL {
				body.WriteString("\\ No newline at end of file\n")
			}
			switch kind {
			case ' ':
				aCount++
				bCount++
			case '-':
				aCount++
			case '+':
				bCount++
			}
		}
		for k := h.start - lead; k < h.start; k++ {
			emit(' ', ops[k].text)
		}
		for ; opAt < h.end; opAt++ {
			op := ops[opAt]
			switch op.kind {
			case ' ':
				emit(' ', op.text)
				aLine++
				bLine++
			case '-':
				emit('-', op.text)
				aLine++
			case '+':
				emit('+', op.text)
				bLine++
			}
		}
		trail := 0
		for opAt+trail < len(ops) && ops[opAt+trail].kind == ' ' && trail < ctx {
			emit(' ', ops[opAt+trail].text)
			trail++
		}
		aLine += trail
		bLine += trail
		opAt += trail
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", hunkRange(aStart, aCount), hunkRange(bStart, bCount))
		sb.WriteString(body.String())
	}
	return sb.String()
}

// hunkRange formats a start,count pair the way git does: a zero-length side
// reports the line *before* the hunk, and ",1" is omitted.
func hunkRange(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

type diffOp struct {
	kind byte // ' ' equal, '-' only in a, '+' only in b
	text string
}

// noEOLSentinel marks a final line that is not newline-terminated. It makes
// "c" and "c\n" compare unequal (git treats them as different lines) and
// tells the emitter to print the "\ No newline at end of file" marker.
const noEOLSentinel = "\x00noeol"

// splitDiffLines splits content into lines without trailing newlines; a
// trailing newline does not create a phantom empty last line, and a missing
// one tags the final line with noEOLSentinel.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	noEOL := !strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	if noEOL {
		lines[len(lines)-1] += noEOLSentinel
	}
	return lines
}

func diffOps(a, b []string) []diffOp {
	// Trim common prefix and suffix; the LCS only runs over the middle.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am := a[pre : len(a)-suf]
	bm := b[pre : len(b)-suf]

	var ops []diffOp
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	if len(am)*len(bm) > 4_000_000 {
		// Degenerate middle: emit one replacement block.
		for _, l := range am {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range bm {
			ops = append(ops, diffOp{'+', l})
		}
	} else {
		ops = append(ops, lcsOps(am, bm)...)
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// dp[i][j] = LCS length of a[i:], b[j:]
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}
