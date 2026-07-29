package lint

import "path/filepath"

// Baseline records the outcome of comparing current findings against the
// findings at a base git ref. In baseline mode only findings introduced
// since the ref are reported (and gate the exit code); pre-existing ones
// are hidden, and baseline findings that no longer occur count as fixed.
type Baseline struct {
	Ref    string `json:"ref"`
	Hidden int    `json:"hidden"` // pre-existing findings suppressed from the report
	Fixed  int    `json:"fixed"`  // baseline findings no longer present
}

// DiffFindings splits current findings into those already present at the
// baseline (hidden) and those introduced since (returned, order preserved).
// Findings are matched as a multiset keyed by rule + workflow file basename
// + message — not line number — so unrelated edits that shift lines do not
// produce false "new" findings. Basenames are used because local findings
// carry a directory prefix while baseline findings come straight from git;
// workflow files all live flat in .github/workflows, so basenames are
// unambiguous. Baseline findings with no current match are counted as fixed.
func DiffFindings(current, baseline []Finding) (newFindings []Finding, hidden, fixed int) {
	key := func(f Finding) string {
		return f.Rule + "\x00" + filepath.Base(f.File) + "\x00" + f.Message
	}
	base := make(map[string]int, len(baseline))
	for _, f := range baseline {
		base[key(f)]++
	}
	newFindings = []Finding{}
	for _, f := range current {
		k := key(f)
		if base[k] > 0 {
			base[k]--
			hidden++
			continue
		}
		newFindings = append(newFindings, f)
	}
	for _, n := range base {
		fixed += n
	}
	return newFindings, hidden, fixed
}
