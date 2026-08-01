package report

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// annotationsPerTypeCap mirrors GitHub's UI limit: at most 10 annotations of
// each type (notice/warning/error) are shown per step. Emitting more is
// wasted noise, so the remainder is summarized in a single notice instead.
const annotationsPerTypeCap = 10

// Annotations writes GitHub Actions workflow commands (::warning / ::notice
// lines) for static findings, so they surface as inline annotations on the
// PR diff and in the run summary when gha-doctor runs inside Actions. No
// code-scanning/SARIF setup is needed — the runner parses these directly
// from the step's output.
//
// Fileless findings are skipped: an annotation needs a file to attach to.
//
// The runner resolves annotation paths against the workspace root (the
// checkout), so baseDirs is tried in order until one yields a clean
// relative path — callers pass the current directory first (equal to the
// workspace root inside a checkout step, and it keeps `--dir sub/repo`
// paths workspace-relative) and the scan dir as a fallback.
func Annotations(w io.Writer, baseDirs []string, findings []lint.Finding) {
	counts := map[string]int{}
	skipped := 0
	for _, f := range findings {
		if f.File == "" {
			continue // repo-level finding: no file to annotate
		}
		cmd := "notice"
		if f.Severity == lint.Warn {
			cmd = "warning"
		}
		if counts[cmd] >= annotationsPerTypeCap {
			skipped++
			continue
		}
		counts[cmd]++
		msg := f.Message
		if f.Advice != "" {
			msg += " — fix: " + f.Advice
		}
		title := "gha-doctor " + f.Rule
		if m, ok := lint.RuleMeta[f.Rule]; ok {
			title += " " + m.Name
		}
		uri := filepath.ToSlash(f.File)
		for _, base := range baseDirs {
			if base == "" {
				continue
			}
			if rel, err := filepath.Rel(base, f.File); err == nil && !strings.HasPrefix(rel, "..") {
				uri = filepath.ToSlash(rel)
				break
			}
		}
		fmt.Fprintf(w, "::%s file=%s,line=%d,title=%s::%s\n",
			cmd, escapeProperty(uri), max(f.Line, 1),
			escapeProperty(title), escapeData(msg))
	}
	if skipped > 0 {
		fmt.Fprintf(w, "::notice title=gha-doctor::%s\n", escapeData(fmt.Sprintf(
			"%d more finding(s) were not annotated (GitHub shows at most %d annotations per type) — see the report output for the full list",
			skipped, annotationsPerTypeCap)))
	}
}

// relArtifactURI makes a finding path repo-relative (slash-separated), the
// same way SARIF artifact URIs are derived.
func relArtifactURI(baseDir, file string) string {
	uri := file
	if baseDir != "" {
		if rel, err := filepath.Rel(baseDir, file); err == nil && !strings.HasPrefix(rel, "..") {
			uri = rel
		}
	}
	return filepath.ToSlash(uri)
}

// escapeData escapes a workflow-command message per the runner's rules.
func escapeData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// escapeProperty escapes a workflow-command property value (file, title).
func escapeProperty(s string) string {
	s = escapeData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}
