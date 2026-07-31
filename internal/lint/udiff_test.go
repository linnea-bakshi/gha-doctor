package lint

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestUnifiedDiffIdentical(t *testing.T) {
	if d := UnifiedDiff("a/f", "b/f", "x\ny\n", "x\ny\n", 3); d != "" {
		t.Errorf("identical inputs should produce empty diff, got %q", d)
	}
}

// Exact-output cases, each verified against `git diff --no-index -U3`.
func TestUnifiedDiffExact(t *testing.T) {
	cases := []struct {
		name, a, b, want string
	}{
		{
			name: "replace middle line",
			a:    "a\nb\nc\nd\ne\nf\ng\nh\n",
			b:    "a\nb\nc\nX\ne\nf\ng\nh\n",
			want: "--- a/f\n+++ b/f\n@@ -1,7 +1,7 @@\n a\n b\n c\n-d\n+X\n e\n f\n g\n",
		},
		{
			name: "insert at start",
			a:    "a\nb\n",
			b:    "z\na\nb\n",
			want: "--- a/f\n+++ b/f\n@@ -1,2 +1,3 @@\n+z\n a\n b\n",
		},
		{
			name: "delete only line",
			a:    "x\n",
			b:    "",
			want: "--- a/f\n+++ b/f\n@@ -1 +0,0 @@\n-x\n",
		},
		{
			name: "no newline gains one plus a line",
			a:    "a\nb\nc",
			b:    "a\nb\nc\nd\n",
			want: "--- a/f\n+++ b/f\n@@ -1,3 +1,4 @@\n a\n b\n-c\n\\ No newline at end of file\n+c\n+d\n",
		},
		{
			name: "no newline on both sides, change above",
			a:    "a\nb\nc",
			b:    "a\nB\nc",
			want: "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n\\ No newline at end of file\n",
		},
		{
			name: "two separate hunks",
			a:    "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n",
			b:    "1\nX\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\nY\n15\n",
			want: "--- a/f\n+++ b/f\n@@ -1,5 +1,5 @@\n 1\n-2\n+X\n 3\n 4\n 5\n@@ -11,5 +11,5 @@\n 11\n 12\n 13\n-14\n+Y\n 15\n",
		},
		{
			name: "close changes merge into one hunk",
			a:    "1\n2\n3\n4\n5\n6\n7\n8\n9\n",
			b:    "1\nX\n3\n4\n5\n6\n7\nY\n9\n",
			want: "--- a/f\n+++ b/f\n@@ -1,9 +1,9 @@\n 1\n-2\n+X\n 3\n 4\n 5\n 6\n 7\n-8\n+Y\n 9\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UnifiedDiff("a/f", "b/f", tc.a, tc.b, 3)
			if got != tc.want {
				t.Errorf("diff mismatch\n got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// applyUnified reconstructs b from a plus the diff — a hermetic stand-in for
// patch(1). It verifies context and deleted lines against a as it goes, so a
// malformed diff fails loudly instead of "applying".
func applyUnified(t testing.TB, a, diff string) string {
	t.Helper()
	var aLines []string
	if a != "" {
		aLines = strings.Split(strings.TrimSuffix(a, "\n"), "\n")
	}
	var out []string
	aPos := 0 // 0-based index into aLines
	bNoEOL := false
	aEndsNL := strings.HasSuffix(a, "\n")
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(lines[i], "--- ") || strings.HasPrefix(lines[i], "+++ ")) {
		i++
	}
	for i < len(lines) {
		var aStart, aCount, bStart, bCount int
		h := lines[i]
		if !strings.HasPrefix(h, "@@") {
			t.Fatalf("expected hunk header, got %q", h)
		}
		parseRange := func(s string) (int, int) {
			var st, ct int
			if n, _ := fmt.Sscanf(s, "%d,%d", &st, &ct); n == 2 {
				return st, ct
			}
			fmt.Sscanf(s, "%d", &st)
			return st, 1
		}
		parts := strings.Fields(h)
		aStart, aCount = parseRange(strings.TrimPrefix(parts[1], "-"))
		bStart, bCount = parseRange(strings.TrimPrefix(parts[2], "+"))
		_ = bStart
		_ = bCount
		if aCount == 0 {
			aStart++ // zero-length ranges name the line before
		}
		for aPos < aStart-1 {
			out = append(out, aLines[aPos])
			aPos++
		}
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@") {
			l := lines[i]
			switch {
			case strings.HasPrefix(l, "\\"):
				// The marker refers to the preceding line: after '-' it is
				// a's ending only; after ' ' or '+' the line is also b's
				// last line, so b lacks a trailing newline.
				if !strings.HasPrefix(lines[i-1], "-") {
					bNoEOL = true
				}
			case strings.HasPrefix(l, " "):
				if aLines[aPos] != l[1:] {
					t.Fatalf("context mismatch at a line %d: file has %q, diff has %q", aPos+1, aLines[aPos], l[1:])
				}
				out = append(out, aLines[aPos])
				aPos++
			case strings.HasPrefix(l, "-"):
				if aLines[aPos] != l[1:] {
					t.Fatalf("delete mismatch at a line %d: file has %q, diff has %q", aPos+1, aLines[aPos], l[1:])
				}
				aPos++
			case strings.HasPrefix(l, "+"):
				out = append(out, l[1:])
			default:
				t.Fatalf("unexpected diff line %q", l)
			}
			i++
		}
	}
	tailStart := aPos
	for aPos < len(aLines) {
		out = append(out, aLines[aPos])
		aPos++
	}
	if tailStart < len(aLines) {
		bNoEOL = !aEndsNL // untouched tail keeps a's ending
	}
	res := strings.Join(out, "\n")
	if len(out) > 0 && !bNoEOL {
		res += "\n"
	}
	return res
}

func TestUnifiedDiffRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"a\nb\nc\n", "a\nX\nc\n"},
		{"", "a\nb\n"},
		{"a\nb\n", ""},
		{"a\nb\nc", "a\nb\nc\nd\n"},
		{"x\ny\nz", "x\ny\nz2"},
		{"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", "1\nA\n3\n4\n5\n6\n7\n8\nB\n10\n"},
	}
	rng := rand.New(rand.NewSource(42))
	vocab := []string{"alpha", "beta", "gamma", "delta", "", "  indent", "x"}
	for n := 0; n < 200; n++ {
		mk := func() string {
			var sb strings.Builder
			for i := 0; i < rng.Intn(30); i++ {
				sb.WriteString(vocab[rng.Intn(len(vocab))])
				sb.WriteByte('\n')
			}
			s := sb.String()
			if rng.Intn(4) == 0 {
				s = strings.TrimSuffix(s, "\n")
			}
			return s
		}
		cases = append(cases, [2]string{mk(), mk()})
	}
	for i, c := range cases {
		a, b := c[0], c[1]
		d := UnifiedDiff("a/f", "b/f", a, b, 3)
		if a == b {
			if d != "" {
				t.Fatalf("case %d: equal inputs gave non-empty diff", i)
			}
			continue
		}
		if got := applyUnified(t, a, d); got != b {
			t.Fatalf("case %d: round-trip failed\n a=%q\n b=%q\n got=%q\n diff:\n%s", i, a, b, got, d)
		}
	}
}

func TestUnifiedDiffHugeFileFallback(t *testing.T) {
	// A middle beyond the DP cap degrades to one replace block but must
	// still round-trip.
	var a, b strings.Builder
	a.WriteString("same\n")
	b.WriteString("same\n")
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&a, "a%d\n", i)
		fmt.Fprintf(&b, "b%d\n", i)
	}
	a.WriteString("tail\n")
	b.WriteString("tail\n")
	d := UnifiedDiff("a/f", "b/f", a.String(), b.String(), 3)
	if d == "" {
		t.Fatal("expected non-empty diff")
	}
	if got := applyUnified(t, a.String(), d); got != b.String() {
		t.Fatal("huge-file fallback did not round-trip")
	}
}

// FuzzUnifiedDiff checks the core invariant on arbitrary inputs: applying
// the produced diff to a reconstructs b exactly (or the diff is empty and
// a == b).
func FuzzUnifiedDiff(f *testing.F) {
	f.Add("a\nb\nc\n", "a\nX\nc\n")
	f.Add("", "x")
	f.Add("x\ny", "x\ny\n")
	f.Add("1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\nA\n3\n4\n5\n6\n7\n8\nB\n")
	f.Fuzz(func(t *testing.T, a, b string) {
		// The diff format is line-based; a lone \r inside a "line" is fine
		// (it is just a byte), so no input filtering is needed.
		d := UnifiedDiff("a/f", "b/f", a, b, 3)
		if a == b {
			if d != "" {
				t.Fatalf("equal inputs produced non-empty diff:\n%s", d)
			}
			return
		}
		if d == "" {
			t.Fatalf("different inputs produced empty diff\n a=%q\n b=%q", a, b)
		}
		if got := applyUnified(t, a, d); got != b {
			t.Fatalf("round-trip failed\n a=%q\n b=%q\n got=%q\n diff:\n%s", a, b, got, d)
		}
	})
}
