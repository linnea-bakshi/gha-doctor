package lint

import (
	"regexp"
	"strings"
)

// Inline suppression: put a comment on the flagged line (or the line
// directly above it):
//
//   - uses: actions/cache@v4  # gha-doctor: ignore[D008]
//
//     # gha-doctor: ignore[D003,D008]
//
//   - uses: actions/setup-node@v4
//
// A bare `# gha-doctor: ignore` suppresses every rule on that line.
// Rule IDs are case-insensitive.
var ignoreRe = regexp.MustCompile(`#\s*gha-doctor:\s*ignore(?:\[([A-Za-z0-9_,\s]*)\])?`)

// directive is one parsed ignore comment.
type directive struct {
	rules       []string // nil = all rules
	commentOnly bool     // line holds nothing but the comment
}

// ignoreSet maps a 1-based line number to the directive on that line.
type ignoreSet map[int]directive

// parseIgnores scans raw file content for ignore directives.
func parseIgnores(data []byte) ignoreSet {
	set := ignoreSet{}
	for i, line := range strings.Split(string(data), "\n") {
		m := ignoreRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var rules []string
		if m[1] != "" {
			for _, r := range strings.Split(m[1], ",") {
				if r = strings.ToUpper(strings.TrimSpace(r)); r != "" {
					rules = append(rules, r)
				}
			}
		}
		set[i+1] = directive{
			rules:       rules, // nil (bare or empty brackets) = all rules
			commentOnly: strings.TrimSpace(line[:strings.Index(line, "#")]) == "",
		}
	}
	return set
}

// matches reports whether a finding for rule at line is suppressed. A
// directive suppresses findings on its own line; a comment-only directive
// line also suppresses findings on the line directly below it.
func (s ignoreSet) matches(line int, rule string) bool {
	if d, ok := s[line]; ok && d.covers(rule) {
		return true
	}
	if d, ok := s[line-1]; ok && d.commentOnly && d.covers(rule) {
		return true
	}
	return false
}

func (d directive) covers(rule string) bool {
	if len(d.rules) == 0 {
		return true
	}
	for _, r := range d.rules {
		if strings.EqualFold(r, rule) {
			return true
		}
	}
	return false
}
