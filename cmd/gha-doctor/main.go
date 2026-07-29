// Command gha-doctor diagnoses GitHub Actions workflows: static
// performance/cost/reliability checks plus run-history analysis
// (flaky jobs, wasted minutes, slow steps).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/completion"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
	"github.com/linnea-bakshi/gha-doctor/internal/report"
)

var version = "dev"

func main() {
	var (
		repoFlag    = flag.String("repo", "", "owner/name to analyze (default: detect from git remote)")
		orgFlag     = flag.String("org", "", "scan a whole org (or user): run-level stats per repo, one API call per repo")
		maxRepos    = flag.Int("max-repos", 20, "with --org: max repos to scan (most recently pushed first)")
		runsFlag    = flag.Int("runs", 100, "number of recent runs to sample for history analysis")
		cacheLogs   = flag.Int("cache-logs", 0, "sample N job logs to measure the real cache hit/miss rate (1 API request per job; needs auth)")
		lintOnly    = flag.Bool("lint-only", false, "only run static workflow checks (no API calls)")
		jsonOut     = flag.Bool("json", false, "output JSON")
		mdOut       = flag.Bool("md", false, "output Markdown (for pasting into an issue)")
		sarifOut    = flag.Bool("sarif", false, "output SARIF 2.1.0 (static findings only; upload to GitHub code scanning)")
		dirFlag     = flag.String("dir", ".", "repository directory to scan")
		fixFlag     = flag.Bool("fix", false, "auto-fix fixable findings (D001/D002/D003/D008/D012) in place; review with git diff")
		disableFlag = flag.String("disable", "", "comma-separated rule IDs to disable, e.g. D004,D009 (inline: # gha-doctor: ignore[D004])")
		badgeFlag   = flag.String("badge", "", "write an SVG health-score badge (shields-style) to this file")
		versionFlag = flag.Bool("version", false, "print version")
		explainFlag = flag.String("explain", "", "print the documentation for a rule and exit, e.g. --explain D004")
		complFlag   = flag.String("completion", "", "print a shell completion script and exit (bash, zsh, or fish)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `gha-doctor %s — diagnose your GitHub Actions

Usage: gha-doctor [flags]

Run inside a repo (or pass --repo owner/name). Static checks read
.github/workflows; history analysis uses the GitHub API with your
GITHUB_TOKEN or gh CLI auth.

Flags:
`, version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *versionFlag {
		fmt.Println("gha-doctor", version)
		return
	}

	if *complFlag != "" {
		if err := completion.Script(os.Stdout, *complFlag, flag.CommandLine); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *explainFlag != "" {
		if err := report.Explain(os.Stdout, *explainFlag, report.AutoStyle()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// Org-wide scan: fleet triage view, no local files involved.
	if *orgFlag != "" {
		c := api.NewClient()
		progress := func(msg string) {
			if !*jsonOut && !*mdOut {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
		oa, err := c.AnalyzeOrg(*orgFlag, *maxRepos, *runsFlag, progress)
		if err != nil {
			fmt.Fprintln(os.Stderr, "org scan failed:", err)
			os.Exit(1)
		}
		switch {
		case *jsonOut:
			if err := report.OrgJSON(os.Stdout, oa); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		case *mdOut:
			report.OrgMarkdown(os.Stdout, oa)
		default:
			report.Org(os.Stdout, report.AutoStyle(), oa)
		}
		return
	}

	// Static lint
	wfDir := filepath.Join(*dirFlag, ".github", "workflows")
	if *fixFlag {
		if fi, err := os.Stat(wfDir); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "no workflows found at %s\n", wfDir)
			os.Exit(1)
		}
		results, err := lint.FixDir(wfDir, *dirFlag, splitRules(*disableFlag))
		if err != nil {
			fmt.Fprintln(os.Stderr, "fix failed:", err)
			os.Exit(1)
		}
		applied := 0
		for _, r := range results {
			for _, a := range r.Applied {
				fmt.Printf("fixed  %s  %s\n", r.Path, a)
				applied++
			}
			for _, s := range r.Skipped {
				fmt.Printf("skip   %s  %s\n", r.Path, s)
			}
		}
		if applied == 0 {
			fmt.Println("nothing to fix (fixable rules: " + strings.Join(lint.FixableRules, ", ") + ")")
		} else {
			fmt.Printf("%d fix(es) applied — review with `git diff`\n", applied)
		}
		return
	}
	var findings []lint.Finding
	filesScanned := 0
	if fi, err := os.Stat(wfDir); err == nil && fi.IsDir() {
		var err error
		findings, filesScanned, err = lint.LintDir(wfDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error scanning workflows:", err)
			os.Exit(1)
		}
		findings = dropDisabled(findings, splitRules(*disableFlag))
	} else if *lintOnly {
		fmt.Fprintf(os.Stderr, "no workflows found at %s\n", wfDir)
		os.Exit(1)
	}

	// History analysis
	var analysis *api.Analysis
	if *sarifOut {
		*lintOnly = true // SARIF carries static findings only
	}
	if !*lintOnly {
		owner, name, err := resolveRepo(*repoFlag, *dirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot determine repo (%v); running static checks only\n", err)
		} else {
			c := api.NewClient()
			c.CacheLogSample = *cacheLogs
			progress := func(msg string) {
				if !*jsonOut && !*mdOut {
					fmt.Fprintln(os.Stderr, msg)
				}
			}
			analysis, err = c.Analyze(owner, name, *runsFlag, progress)
			if err != nil {
				fmt.Fprintln(os.Stderr, "history analysis failed:", err)
				if len(findings) == 0 && filesScanned == 0 {
					os.Exit(1)
				}
			}
		}
	}

	score := report.ComputeScore(findings, filesScanned, analysis)
	var scorePtr *report.Score
	if len(score.Components) > 0 {
		scorePtr = &score
	}
	if *badgeFlag != "" {
		if err := writeBadge(*badgeFlag, score); err != nil {
			fmt.Fprintln(os.Stderr, "badge:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "badge written to %s (%s, %d/100)\n", *badgeFlag, score.Grade, score.Points)
	}

	switch {
	case *sarifOut:
		if err := report.SARIF(os.Stdout, version, *dirFlag, findings); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case *jsonOut:
		if err := report.JSON(os.Stdout, findings, analysis, scorePtr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case *mdOut:
		report.Markdown(os.Stdout, findings, filesScanned, analysis, scorePtr)
	default:
		s := report.AutoStyle()
		report.Findings(os.Stdout, s, findings, filesScanned)
		if analysis != nil {
			report.Analysis(os.Stdout, s, analysis)
		}
		if scorePtr != nil {
			report.ScoreSection(os.Stdout, s, score)
		}
	}

	for _, f := range findings {
		if f.Severity == lint.Warn {
			os.Exit(2) // warnings found: useful for CI gating
		}
	}
}

var sshRe = regexp.MustCompile(`^(?:ssh://)?git@github\.com[:/]([^/]+)/(.+?)(?:\.git)?$`)
var httpsRe = regexp.MustCompile(`^https://github\.com/([^/]+)/(.+?)(?:\.git)?$`)

// resolveRepo determines owner/name from the flag or the git remote.
func resolveRepo(repoFlag, dir string) (string, string, error) {
	if repoFlag != "" {
		parts := strings.SplitN(repoFlag, "/", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("--repo must be owner/name")
		}
		return parts[0], parts[1], nil
	}
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("no --repo given and no git remote found")
	}
	url := strings.TrimSpace(string(out))
	for _, re := range []*regexp.Regexp{sshRe, httpsRe} {
		if m := re.FindStringSubmatch(url); m != nil {
			return m[1], m[2], nil
		}
	}
	return "", "", fmt.Errorf("remote %q is not a github.com URL", url)
}

// writeBadge renders the health-score badge SVG to path.
func writeBadge(path string, sc report.Score) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := report.Badge(f, sc); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// splitRules parses a comma-separated rule ID list, normalizing case.
func splitRules(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, r := range strings.Split(v, ",") {
		if r = strings.ToUpper(strings.TrimSpace(r)); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// dropDisabled removes findings whose rule ID is in disabled.
func dropDisabled(fs []lint.Finding, disabled []string) []lint.Finding {
	if len(disabled) == 0 {
		return fs
	}
	off := map[string]bool{}
	for _, r := range disabled {
		off[r] = true
	}
	kept := fs[:0]
	for _, f := range fs {
		if !off[strings.ToUpper(f.Rule)] {
			kept = append(kept, f)
		}
	}
	return kept
}
