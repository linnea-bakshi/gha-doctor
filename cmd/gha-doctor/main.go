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
	"time"

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
		runFlag     = flag.String("run", "", "deep-dive one workflow run: job waterfall + step timings vs the workflow's own p50s (run ID, URL, or 'latest')")
		cacheLogs   = flag.Int("cache-logs", 0, "sample N job logs to measure the real cache hit/miss rate (1 API request per job; needs auth)")
		lintOnly    = flag.Bool("lint-only", false, "only run static workflow checks (no API calls)")
		jsonOut     = flag.Bool("json", false, "output JSON")
		mdOut       = flag.Bool("md", false, "output Markdown (for pasting into an issue)")
		sarifOut    = flag.Bool("sarif", false, "output SARIF 2.1.0 (static findings only; upload to GitHub code scanning)")
		dirFlag     = flag.String("dir", ".", "repository directory to scan")
		fixFlag     = flag.Bool("fix", false, "auto-fix fixable findings (D001/D002/D003/D008/D012) in place; review with git diff")
		disableFlag = flag.String("disable", "", "comma-separated rule IDs to disable, e.g. D004,D009 (inline: # gha-doctor: ignore[D004])")
		baseFlag    = flag.String("baseline", "", "git ref to compare against (e.g. origin/main): report and gate only on findings introduced since that ref")
		badgeFlag   = flag.String("badge", "", "write an SVG health-score badge (shields-style) to this file")
		svgFlag     = flag.String("svg", "", "with --org: write an SVG fleet card (embeddable in a profile README) to this file")
		scoreHist   = flag.String("score-history", "", "append the score to this JSONL file and report the change since the last run (commit it to track trends)")
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
	// Exit 2 is reserved for "warnings found" (CI gating); a bad flag must
	// not look like findings, so usage errors exit 1 instead of the flag
	// package's default of 2.
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

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

	// Single-run deep dive: timeline + step timings vs history.
	if *runFlag != "" {
		owner, name, err := resolveRepo(*repoFlag, *dirFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot determine repo:", err)
			os.Exit(1)
		}
		id, latest, err := api.ParseRunID(*runFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		c := api.NewClient()
		progress := func(msg string) {
			if !*jsonOut && !*mdOut {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
		var run *api.Run
		if latest {
			run, err = c.LatestRun(owner, name)
		} else {
			run, err = c.GetRun(owner, name, id)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "fetching run:", err)
			os.Exit(1)
		}
		deep, err := c.AnalyzeRun(owner, name, run, progress)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run analysis failed:", err)
			os.Exit(1)
		}
		switch {
		case *jsonOut:
			if err := report.RunDeepJSON(os.Stdout, deep); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		case *mdOut:
			report.RunDeepMarkdown(os.Stdout, deep)
		default:
			report.RunDeep(os.Stdout, report.AutoStyle(), deep)
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
		if *svgFlag != "" {
			if err := writeOrgSVG(*svgFlag, oa); err != nil {
				fmt.Fprintln(os.Stderr, "svg:", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "fleet card written to %s (%d repos)\n", *svgFlag, len(oa.Repos))
		}
		return
	}
	if *svgFlag != "" {
		fmt.Fprintln(os.Stderr, "--svg requires --org (for a single repo's badge, use --badge)")
		os.Exit(1)
	}

	// Remote mode: --repo names a repo that is not the current directory,
	// and --dir was not explicitly given. Static checks then run against the
	// workflow files fetched from that repo, not whatever happens to be in
	// the cwd (which would silently grade the wrong repo's hygiene).
	dirSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "dir" {
			dirSet = true
		}
	})
	remoteLint := false
	if *repoFlag != "" && !dirSet {
		lo, ln, err := resolveRepo("", *dirFlag)
		if err != nil || !strings.EqualFold(lo+"/"+ln, strings.TrimSuffix(*repoFlag, ".git")) {
			remoteLint = true
		}
	}
	if remoteLint && *fixFlag {
		fmt.Fprintf(os.Stderr, "refusing to --fix: --repo %s does not match this directory's git remote.\n"+
			"--fix edits local files; run it inside that repo's checkout (or pass --dir).\n", *repoFlag)
		os.Exit(1)
	}
	if *baseFlag != "" && remoteLint {
		fmt.Fprintf(os.Stderr, "--baseline needs a local git checkout (it reads workflows from the ref via git); "+
			"run it inside the repo instead of --repo %s\n", *repoFlag)
		os.Exit(1)
	}
	if *baseFlag != "" && *fixFlag {
		fmt.Fprintln(os.Stderr, "--baseline has no effect with --fix; run them separately")
		os.Exit(1)
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
	if remoteLint {
		c := api.NewClient()
		owner, name, _ := resolveRepo(*repoFlag, *dirFlag)
		files, truncated, err := c.ListWorkflowFiles(owner, name)
		if err != nil {
			if _, ok := err.(*api.NotFoundError); ok {
				fmt.Fprintf(os.Stderr, "no .github/workflows directory in %s\n", *repoFlag)
			} else {
				fmt.Fprintln(os.Stderr, "fetching workflows:", err)
			}
			if *lintOnly {
				os.Exit(1)
			}
		} else {
			if truncated && !*jsonOut && !*mdOut {
				fmt.Fprintf(os.Stderr, "note: %s has a very large number of workflow files; linted the first %d\n", *repoFlag, len(files))
			}
			var named []lint.NamedFile
			for _, f := range files {
				named = append(named, lint.NamedFile{Path: f.Path, Data: f.Data})
			}
			findings, filesScanned = lint.LintFiles(named)
			findings = dropDisabled(findings, splitRules(*disableFlag))
		}
	} else if fi, err := os.Stat(wfDir); err == nil && fi.IsDir() {
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

	// Baseline mode: lint the workflows as they exist at the base ref and
	// keep only findings introduced since. The health score still reflects
	// the whole repo (allFindings); the report and the exit-2 gate use only
	// what this change introduced.
	allFindings := findings
	var baseline *lint.Baseline
	if *baseFlag != "" {
		baseFiles, err := baselineWorkflowFiles(*dirFlag, *baseFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baseline:", err)
			os.Exit(1)
		}
		baseFindings, _ := lint.LintFiles(baseFiles)
		baseFindings = dropDisabled(baseFindings, splitRules(*disableFlag))
		var hidden, fixed int
		findings, hidden, fixed = lint.DiffFindings(findings, baseFindings)
		baseline = &lint.Baseline{Ref: *baseFlag, Hidden: hidden, Fixed: fixed}
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

	score := report.ComputeScore(allFindings, filesScanned, analysis)
	var trend []int // past + current points for the badge sparkline
	if *scoreHist != "" {
		if len(score.Components) == 0 {
			fmt.Fprintln(os.Stderr, "score-history: nothing was scored; not recording an entry")
		} else {
			repoID := ""
			if o, n, err := resolveRepo(*repoFlag, *dirFlag); err == nil {
				repoID = o + "/" + n
			}
			entries, bad, err := report.LoadHistory(*scoreHist)
			if err != nil {
				fmt.Fprintln(os.Stderr, "score-history:", err)
				os.Exit(1)
			}
			if bad > 0 {
				fmt.Fprintf(os.Stderr, "score-history: skipped %d unparseable line(s) in %s\n", bad, *scoreHist)
			}
			if prev, ok := report.LatestFor(entries, repoID); ok {
				score.Delta = report.DeltaFrom(prev, score)
			}
			trend = append(report.PointsFor(entries, repoID), score.Points)
			if err := report.AppendHistory(*scoreHist, report.EntryFor(score, repoID, time.Now())); err != nil {
				fmt.Fprintln(os.Stderr, "score-history:", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "score recorded in %s (%d entr%s)\n", *scoreHist, len(entries)+1, pluralIES(len(entries)+1))
		}
	}
	var scorePtr *report.Score
	if len(score.Components) > 0 {
		scorePtr = &score
	}
	if *badgeFlag != "" {
		if err := writeBadge(*badgeFlag, score, trend); err != nil {
			fmt.Fprintln(os.Stderr, "badge:", err)
			os.Exit(1)
		}
		extra := ""
		if len(trend) >= 2 {
			extra = fmt.Sprintf(", %d-run trend", len(trend))
		}
		fmt.Fprintf(os.Stderr, "badge written to %s (%s, %d/100%s)\n", *badgeFlag, score.Grade, score.Points, extra)
	}

	wins := report.ComputeWins(findings, analysis, time.Now())

	switch {
	case *sarifOut:
		if err := report.SARIF(os.Stdout, version, *dirFlag, findings); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case *jsonOut:
		if err := report.JSON(os.Stdout, findings, baseline, analysis, scorePtr, wins); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case *mdOut:
		report.Markdown(os.Stdout, findings, filesScanned, baseline, analysis, scorePtr, wins)
	default:
		s := report.AutoStyle()
		report.Findings(os.Stdout, s, findings, filesScanned, baseline)
		if analysis != nil {
			report.Analysis(os.Stdout, s, analysis)
		}
		if scorePtr != nil {
			report.ScoreSection(os.Stdout, s, score)
		}
		report.WinsSection(os.Stdout, s, wins)
	}

	for _, f := range findings {
		if f.Severity == lint.Warn {
			os.Exit(2) // warnings found: useful for CI gating
		}
	}
}

// baselineWorkflowFiles reads .github/workflows/*.yml|yaml as they exist at
// a git ref, without touching the working tree.
func baselineWorkflowFiles(dir, ref string) ([]lint.NamedFile, error) {
	out, err := exec.Command("git", "-C", dir, "ls-tree", "--name-only", ref, ".github/workflows/").Output()
	if err != nil {
		msg := gitStderr(err)
		return nil, fmt.Errorf("git ls-tree %s failed%s — is %q a fetched ref? (in shallow CI checkouts, fetch the base branch first: git fetch origin <branch>)", ref, msg, ref)
	}
	var files []lint.NamedFile
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || (!strings.HasSuffix(line, ".yml") && !strings.HasSuffix(line, ".yaml")) {
			continue
		}
		data, err := exec.Command("git", "-C", dir, "show", ref+":"+line).Output()
		if err != nil {
			return nil, fmt.Errorf("git show %s:%s failed%s", ref, line, gitStderr(err))
		}
		files = append(files, lint.NamedFile{Path: line, Data: data})
	}
	return files, nil
}

func gitStderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return ": " + strings.TrimSpace(string(ee.Stderr))
	}
	return ""
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
func writeBadge(path string, sc report.Score, trend []int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := report.Badge(f, sc, trend); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeOrgSVG renders the org fleet card SVG to path.
func writeOrgSVG(path string, oa *api.OrgAnalysis) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := report.OrgSVG(f, oa, time.Now()); err != nil {
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

func pluralIES(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
