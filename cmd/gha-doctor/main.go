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
	"github.com/linnea-bakshi/gha-doctor/internal/config"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
	"github.com/linnea-bakshi/gha-doctor/internal/report"
)

var version = "dev"

// displayName is how the binary refers to itself in help output. When
// installed as a gh CLI extension the binary is named gh-doctor and users
// invoke it as `gh doctor`, so the usage text should say that.
func displayName() string {
	base := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if base == "gh-doctor" {
		return "gh doctor"
	}
	if base == "" || strings.HasPrefix(base, ".") {
		return "gha-doctor"
	}
	return base
}

func main() {
	var (
		repoFlag    = flag.String("repo", "", "owner/name to analyze (default: detect from git remote)")
		orgFlag     = flag.String("org", "", "scan a whole org (or user): run-level stats per repo, one API call per repo")
		maxRepos    = flag.Int("max-repos", 20, "with --org: max repos to scan (most recently pushed first)")
		runsFlag    = flag.Int("runs", 100, "number of recent runs to sample for history analysis")
		wfFlag      = flag.String("workflow", "", "scope the history analysis to one workflow (file name like ci.yml, full path, or display name); cache/artifact figures stay repo-wide")
		runFlag     = flag.String("run", "", "deep-dive one workflow run: job waterfall + step timings vs the workflow's own p50s (run ID, URL, or 'latest')")
		logTailFlag = flag.Int("log-tail", 20, "with --run: lines of the failing step's log to show per failed job (0 = off; needs auth)")
		cacheLogs   = flag.Int("cache-logs", 0, "sample N job logs to measure the real cache hit/miss rate (1 API request per job; needs auth)")
		flakyLogs   = flag.Int("flaky-logs", 0, "read N flaky-failure job logs to name the flaky tests (1 API request per log; needs auth)")
		lintOnly    = flag.Bool("lint-only", false, "only run static workflow checks (no API calls)")
		jsonOut     = flag.Bool("json", false, "output JSON")
		mdOut       = flag.Bool("md", false, "output Markdown (for pasting into an issue)")
		sarifOut    = flag.Bool("sarif", false, "output SARIF 2.1.0 (static findings only; upload to GitHub code scanning)")
		annotateOut = flag.Bool("annotate", false, "also emit GitHub ::warning/::notice workflow commands for findings — inline PR annotations when run inside Actions, no code-scanning setup needed")
		dirFlag     = flag.String("dir", ".", "repository directory to scan")
		fixFlag     = flag.Bool("fix", false, "auto-fix fixable findings ("+strings.Join(lint.FixableRules, "/")+") in place; review with git diff")
		diffFlag    = flag.Bool("diff", false, "preview what --fix would change as a unified diff, without writing (works with --repo on any repo, no clone needed)")
		disableFlag = flag.String("disable", "", "comma-separated rule IDs to disable, e.g. D004,D009 (inline: # gha-doctor: ignore[D004])")
		failOnFlag  = flag.String("fail-on", config.FailWarn, "minimum finding severity that makes the exit code 2 for CI gating: any, warning, or never (report-only)")
		noConfig    = flag.Bool("no-config", false, "ignore the repo's .gha-doctor.yml config file")
		baseFlag    = flag.String("baseline", "", "git ref to compare against (e.g. origin/main): report and gate only on findings introduced since that ref")
		badgeFlag   = flag.String("badge", "", "write an SVG health-score badge (shields-style) to this file")
		htmlFlag    = flag.String("html", "", "write a self-contained HTML report to this file (works with --run and --org too; publish as a CI artifact or Pages)")
		promFlag    = flag.String("prom", "", "write the report's aggregates in Prometheus text format to this file ('-' = stdout); run on a schedule + a textfile collector to graph CI health over time")
		svgFlag     = flag.String("svg", "", "with --org: write an SVG fleet card (embeddable in a profile README) to this file")
		scoreHist   = flag.String("score-history", "", "append the score to this JSONL file and report the change since the last run (commit it to track trends)")
		initFlag    = flag.Bool("init", false, "write a ready-to-commit "+initRelPath+" that runs gha-doctor on every PR (baseline-gated, sticky comment) and exit")
		mcpFlag     = flag.Bool("mcp", false, "run as an MCP (Model Context Protocol) stdio server exposing read-only diagnose tools to AI agents")
		versionFlag = flag.Bool("version", false, "print version")
		explainFlag = flag.String("explain", "", "print the documentation for a rule and exit, e.g. --explain D004")
		complFlag   = flag.String("completion", "", "print a shell completion script and exit (bash, zsh, or fish)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `%[1]s %[2]s — diagnose your GitHub Actions

Usage: %[1]s [flags]

Run inside a repo (or pass --repo owner/name). Static checks read
.github/workflows; history analysis uses the GitHub API with your
GITHUB_TOKEN or gh CLI auth.

Flags:
`, displayName(), version)
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

	failOn, err := config.ParseFailOn(*failOnFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--fail-on:", err)
		os.Exit(1)
	}

	if *diffFlag {
		conflicts := map[string]bool{
			"--fix": *fixFlag, "--baseline": *baseFlag != "", "--sarif": *sarifOut,
			"--org": *orgFlag != "", "--run": *runFlag != "", "--html": *htmlFlag != "",
			"--badge": *badgeFlag != "", "--score-history": *scoreHist != "", "--prom": *promFlag != "",
		}
		for name, set := range conflicts {
			if set {
				if name == "--fix" {
					fmt.Fprintln(os.Stderr, "--diff previews and --fix applies; use one or the other")
				} else {
					fmt.Fprintf(os.Stderr, "--diff cannot be combined with %s\n", name)
				}
				os.Exit(1)
			}
		}
	}

	if *wfFlag != "" {
		// --workflow scopes the run sample and the static findings to one
		// workflow. Modes that are whole-repo (or no-history) by nature
		// refuse it instead of half-honoring it.
		conflicts := []struct {
			name string
			set  bool
		}{
			{"--org", *orgFlag != ""},
			{"--run", *runFlag != ""},
			{"--lint-only", *lintOnly},
			{"--sarif", *sarifOut},
			{"--fix", *fixFlag},
			{"--diff", *diffFlag},
			{"--baseline", *baseFlag != ""},
		}
		for _, cf := range conflicts {
			if cf.set {
				fmt.Fprintf(os.Stderr, "--workflow scopes the run-history analysis; it cannot be combined with %s\n", cf.name)
				os.Exit(1)
			}
		}
		for _, cf := range []struct {
			name string
			set  bool
		}{
			{"--badge", *badgeFlag != ""},
			{"--score-history", *scoreHist != ""},
		} {
			if cf.set {
				fmt.Fprintf(os.Stderr, "the health score is whole-repo; %s cannot be combined with --workflow (drop --workflow to score)\n", cf.name)
				os.Exit(1)
			}
		}
		if *promFlag != "" {
			// A scoped sample must never wear whole-repo labels: the
			// repo-level gauges (waste, cost, findings) would silently
			// describe one workflow while labeled repo="owner/name".
			fmt.Fprintln(os.Stderr, "--prom metrics describe the whole repo; it cannot be combined with --workflow")
			os.Exit(1)
		}
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

	if *mcpFlag {
		// --mcp turns the process into a protocol server; any other flag
		// alongside it is a confused invocation (tools carry their own
		// arguments per call).
		bad := ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "mcp" {
				bad = f.Name
			}
		})
		if bad != "" {
			fmt.Fprintf(os.Stderr, "--mcp cannot be combined with --%s (tool calls carry their own arguments)\n", bad)
			os.Exit(1)
		}
		os.Exit(runMCP())
	}

	if *initFlag {
		// --init writes a file and exits; any other mode flag alongside it
		// is a confused invocation, not something to half-honor.
		bad := ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "init" && f.Name != "dir" {
				bad = f.Name
			}
		})
		if bad != "" {
			fmt.Fprintf(os.Stderr, "--init cannot be combined with --%s\n", bad)
			os.Exit(1)
		}
		runInit(*dirFlag)
		return
	}

	// Which flags were given explicitly: an explicit CLI flag always beats
	// the config file.
	setFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	dirSet := setFlags["dir"]

	// Remote mode: --repo names a repo that is not the current directory,
	// and --dir was not explicitly given. Static checks then run against the
	// workflow files fetched from that repo, not whatever happens to be in
	// the cwd (which would silently grade the wrong repo's hygiene).
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

	// Repo config (.gha-doctor.yml): the scanned repo's own standing policy.
	// Local scans read it from the checkout; --repo fetches it from the
	// target repo (the same requests also answer D017 and lockfile
	// detection, so config support costs no extra API calls there).
	var cfg *config.Config
	var cfgWarns []string
	var repoMeta *api.RepoMeta
	if *orgFlag == "" {
		if remoteLint && !(*runFlag != "" && *noConfig) {
			if owner, name, err := resolveRepo(*repoFlag, *dirFlag); err == nil {
				m, merr := api.NewClient().FindRepoMeta(owner, name)
				if merr != nil {
					if _, ok := merr.(*api.NotFoundError); !ok {
						fmt.Fprintln(os.Stderr, "note: repo metadata lookup failed:", merr)
					}
				} else {
					repoMeta = m
				}
			}
		}
		if !*noConfig {
			if remoteLint {
				if repoMeta != nil && repoMeta.DoctorConfig != nil {
					c2, warns, cerr := config.Parse(repoMeta.DoctorConfig.Path, repoMeta.DoctorConfig.Data)
					if cerr != nil {
						fmt.Fprintf(os.Stderr, "config: %s in %s: %v — running unconfigured\n", repoMeta.DoctorConfig.Path, *repoFlag, cerr)
					} else {
						cfg, cfgWarns = c2, warns
					}
				}
			} else {
				c2, warns, cerr := config.FindLocal(*dirFlag)
				if cerr != nil {
					fmt.Fprintf(os.Stderr, "config: %v — running unconfigured\n", cerr)
				} else if c2 != nil {
					cfg, cfgWarns = c2, warns
				}
			}
		}
	}
	if cfg != nil {
		for _, w := range cfgWarns {
			fmt.Fprintf(os.Stderr, "config: %s: %s\n", cfg.File, w)
		}
		fmt.Fprintf(os.Stderr, "config: %s — %s\n", cfg.File, cfg.Summary())
		if cfg.Runs != nil && !setFlags["runs"] {
			*runsFlag = *cfg.Runs
		}
		if cfg.CacheLogs != nil && !setFlags["cache-logs"] {
			*cacheLogs = *cfg.CacheLogs
		}
		if cfg.FlakyLogs != nil && !setFlags["flaky-logs"] {
			*flakyLogs = *cfg.FlakyLogs
		}
		if cfg.LogTail != nil && !setFlags["log-tail"] {
			*logTailFlag = *cfg.LogTail
		}
		if cfg.FailOn != nil && !setFlags["fail-on"] {
			failOn = *cfg.FailOn
		}
	}
	effDisable := splitRules(*disableFlag)
	if cfg != nil {
		effDisable = unionRules(effDisable, cfg.Disable)
	}

	// --prom exports the main report's aggregates; other modes have no
	// (or a different) set of aggregates, so refuse instead of silently
	// writing nothing.
	if *promFlag != "" {
		for name, set := range map[string]bool{
			"--run": *runFlag != "", "--org": *orgFlag != "",
			"--sarif": *sarifOut, "--fix": *fixFlag, "--diff": *diffFlag,
		} {
			if set {
				fmt.Fprintf(os.Stderr, "--prom cannot be combined with %s\n", name)
				os.Exit(1)
			}
		}
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
		deep, err := c.AnalyzeRun(owner, name, run, *logTailFlag, progress)
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
		if *htmlFlag != "" {
			var buf strings.Builder
			report.RunDeepMarkdown(&buf, deep)
			writeHTML(*htmlFlag, buf.String(), report.HTMLMeta{
				Title:    fmt.Sprintf("Run #%d · %s — %s", deep.RunNumber, deep.Workflow, deep.Repo),
				Subtitle: htmlSubtitle(),
			})
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
		if *htmlFlag != "" {
			var buf strings.Builder
			report.OrgMarkdown(&buf, oa)
			writeHTML(*htmlFlag, buf.String(), report.HTMLMeta{
				Title:    "Fleet triage — " + oa.Org,
				Subtitle: htmlSubtitle(),
			})
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

	// Static lint
	wfDir := filepath.Join(*dirFlag, ".github", "workflows")
	if *diffFlag {
		var previews []lint.FixPreview
		remoteRepo := ""
		if remoteLint {
			c := api.NewClient()
			owner, name, err := resolveRepo(*repoFlag, *dirFlag)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			remoteRepo = owner + "/" + name
			files, truncated, err := c.ListWorkflowFiles(owner, name)
			if err != nil {
				if _, ok := err.(*api.NotFoundError); ok {
					fmt.Fprintf(os.Stderr, "no .github/workflows directory in %s\n", *repoFlag)
				} else {
					fmt.Fprintln(os.Stderr, "fetching workflows:", err)
				}
				os.Exit(1)
			}
			if truncated && !*jsonOut && !*mdOut {
				fmt.Fprintf(os.Stderr, "note: %s has a very large number of workflow files; previewing the first %d\n", *repoFlag, len(files))
			}
			// Best-effort lockfile detection for the D003 fix; a failed
			// metadata lookup just means "no package manager detected"
			// (that fix degrades to an honest skip).
			pm := map[string]string{}
			if repoMeta != nil {
				pm = lint.DetectPackageManagersFromList(repoMeta.RootFiles)
			}
			var named []lint.NamedFile
			for _, f := range files {
				named = append(named, lint.NamedFile{Path: f.Path, Data: f.Data})
			}
			previews = lint.PreviewFiles(named, pm, effDisable)
		} else {
			if fi, err := os.Stat(wfDir); err != nil || !fi.IsDir() {
				fmt.Fprintf(os.Stderr, "no workflows found at %s\n", wfDir)
				os.Exit(1)
			}
			var err error
			previews, err = lint.PreviewDir(wfDir, *dirFlag, effDisable)
			if err != nil {
				fmt.Fprintln(os.Stderr, "diff preview failed:", err)
				os.Exit(1)
			}
		}
		switch {
		case *jsonOut:
			if err := report.DiffPreviewJSON(os.Stdout, previews); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		case *mdOut:
			report.DiffPreviewMD(os.Stdout, previews, remoteRepo)
		default:
			report.DiffPreview(os.Stdout, report.AutoStyle(), previews, remoteRepo)
		}
		for _, p := range previews {
			if p.Failed != "" {
				os.Exit(1)
			}
		}
		return
	}
	if *fixFlag {
		if fi, err := os.Stat(wfDir); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "no workflows found at %s\n", wfDir)
			os.Exit(1)
		}
		results, err := lint.FixDir(wfDir, *dirFlag, effDisable)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fix failed:", err)
			os.Exit(1)
		}
		applied, failed := 0, 0
		for _, r := range results {
			for _, a := range r.Applied {
				fmt.Printf("fixed  %s  %s\n", r.Path, a)
				applied++
			}
			for _, s := range r.Skipped {
				fmt.Printf("skip   %s  %s\n", r.Path, s)
			}
			if r.Failed != "" {
				fmt.Fprintf(os.Stderr, "fail   %s  %s\n", r.Path, r.Failed)
				failed++
			}
		}
		if applied == 0 {
			fmt.Println("nothing to fix (fixable rules: " + strings.Join(lint.FixableRules, ", ") + ")")
		} else {
			fmt.Printf("%d fix(es) applied — review with `git diff`\n", applied)
		}
		if failed > 0 {
			os.Exit(1)
		}
		return
	}
	var findings []lint.Finding
	var repoLevel []lint.Finding // D017: repo-level, kept out of SARIF and baseline diffs
	filesScanned := 0
	if remoteLint {
		c := api.NewClient()
		owner, name, _ := resolveRepo(*repoFlag, *dirFlag)
		files, truncated, wfErr := c.ListWorkflowFiles(owner, name)
		if wfErr == nil {
			if truncated && !*jsonOut && !*mdOut {
				fmt.Fprintf(os.Stderr, "note: %s has a very large number of workflow files; linted the first %d\n", *repoFlag, len(files))
			}
			var named []lint.NamedFile
			for _, f := range files {
				named = append(named, lint.NamedFile{Path: f.Path, Data: f.Data})
			}
			findings, filesScanned = lint.LintFiles(named)
			findings = dropDisabled(findings, effDisable)
			if filesScanned > 0 && repoMeta != nil {
				// Best-effort: a failed metadata lookup (repoMeta == nil)
				// skips the check rather than inventing a "no automation"
				// finding without evidence.
				var nf *lint.NamedFile
				if db := repoMeta.Dependabot; db != nil {
					nf = &lint.NamedFile{Path: db.Path, Data: db.Data}
				}
				repoLevel = lint.CheckUpdateAutomation(nf, repoMeta.RenovatePath)
			}
		}
		// Action manifests (action.yml) are a second lint surface; a repo
		// that publishes an action may have no workflows at all. Discovery
		// is best-effort: a failed tree call skips it, it never fails the run.
		actFiles, actTrunc, actErr := c.ListActionFiles(owner, name, lint.IsActionPath, lint.MaxActionFiles)
		if actErr == nil && len(actFiles) > 0 {
			var named []lint.NamedFile
			for _, f := range actFiles {
				named = append(named, lint.NamedFile{Path: f.Path, Data: f.Data})
			}
			af, an := lint.LintActionFiles(named)
			findings = append(findings, dropDisabled(af, effDisable)...)
			filesScanned += an
			if actTrunc && !*jsonOut && !*mdOut {
				fmt.Fprintf(os.Stderr, "note: action-manifest discovery in %s was truncated; linted the first %d\n", *repoFlag, an)
			}
		}
		if wfErr != nil {
			if _, ok := wfErr.(*api.NotFoundError); ok {
				if filesScanned == 0 {
					fmt.Fprintf(os.Stderr, "no .github/workflows directory (or action manifests) in %s\n", *repoFlag)
				}
			} else {
				fmt.Fprintln(os.Stderr, "fetching workflows:", wfErr)
			}
			if *lintOnly && filesScanned == 0 {
				os.Exit(1)
			}
		}
	} else {
		wfScanned := 0
		if fi, err := os.Stat(wfDir); err == nil && fi.IsDir() {
			var err error
			findings, wfScanned, err = lint.LintDir(wfDir)
			if err != nil {
				fmt.Fprintln(os.Stderr, "error scanning workflows:", err)
				os.Exit(1)
			}
			findings = dropDisabled(findings, effDisable)
			if wfScanned > 0 {
				repoLevel = lint.CheckUpdateAutomation(lint.FindUpdateConfigLocal(*dirFlag))
			}
		}
		actPaths, _ := lint.DiscoverActionFiles(*dirFlag)
		af, an := lint.LintActionFiles(lint.ReadActionFiles(*dirFlag, actPaths))
		findings = append(findings, dropDisabled(af, effDisable)...)
		filesScanned = wfScanned + an
		if filesScanned == 0 && *lintOnly {
			fmt.Fprintf(os.Stderr, "no workflows (or action manifests) found at %s\n", wfDir)
			os.Exit(1)
		}
	}

	repoLevel = dropDisabled(repoLevel, effDisable)

	// Baseline mode: lint the workflows as they exist at the base ref and
	// keep only findings introduced since. The health score still reflects
	// the whole repo (allFindings); the report and the exit-2 gate use only
	// what this change introduced.
	allFindings := append(findings[:len(findings):len(findings)], repoLevel...)
	var baseline *lint.Baseline
	if *baseFlag != "" {
		baseFiles, err := baselineWorkflowFiles(*dirFlag, *baseFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baseline:", err)
			os.Exit(1)
		}
		baseFindings, _ := lint.LintFiles(baseFiles)
		baseActs, err := baselineActionFiles(*dirFlag, *baseFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baseline:", err)
			os.Exit(1)
		}
		baseActFindings, _ := lint.LintActionFiles(baseActs)
		baseFindings = append(baseFindings, baseActFindings...)
		baseFindings = dropDisabled(baseFindings, effDisable)
		var hidden, fixed int
		findings, hidden, fixed = lint.DiffFindings(findings, baseFindings)
		baseline = &lint.Baseline{Ref: *baseFlag, Hidden: hidden, Fixed: fixed}
	} else if !*sarifOut {
		// D017 is repo-level: it isn't "introduced since REF" (baseline
		// mode omits it) and has no file to annotate (SARIF omits it).
		findings = append(findings, repoLevel...)
	}

	// History analysis
	var analysis *api.Analysis
	var wfScope *api.Workflow
	if *sarifOut {
		*lintOnly = true // SARIF carries static findings only
	}
	if !*lintOnly {
		owner, name, err := resolveRepo(*repoFlag, *dirFlag)
		if err != nil {
			if *wfFlag != "" {
				// The whole invocation is about one workflow's history;
				// degrading to static-only would answer a different question.
				fmt.Fprintf(os.Stderr, "--workflow needs a repo to analyze: %v\n", err)
				os.Exit(1)
			}
			if filesScanned > 0 {
				// With nothing scanned either, the "nothing to scan"
				// message below explains the situation on its own.
				fmt.Fprintf(os.Stderr, "cannot determine repo (%v); running static checks only\n", err)
			}
		} else {
			c := api.NewClient()
			c.CacheLogSample = *cacheLogs
			c.FlakyLogSample = *flakyLogs
			progress := func(msg string) {
				if !*jsonOut && !*mdOut {
					fmt.Fprintln(os.Stderr, msg)
				}
			}
			if *wfFlag != "" {
				wfScope, err = c.ResolveWorkflow(owner, name, *wfFlag)
				if err != nil {
					fmt.Fprintln(os.Stderr, "--workflow:", err)
					os.Exit(1)
				}
			}
			analysis, err = c.Analyze(owner, name, *runsFlag, wfScope, progress)
			if err != nil {
				fmt.Fprintln(os.Stderr, "history analysis failed:", err)
				if len(findings) == 0 && filesScanned == 0 {
					os.Exit(1)
				}
			}
		}
	}

	// --workflow narrows the static findings to that workflow's own file,
	// so the report reads as one workflow's checkup end to end. Repo-level
	// findings (D017) and every other file are out of scope by definition;
	// dynamically-provided workflows (e.g. pages builds) have no lintable
	// file at all.
	if wfScope != nil {
		base := wfScope.Path
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		var kept []lint.Finding
		for _, f := range findings {
			if filepath.Base(f.File) == base {
				kept = append(kept, f)
			}
		}
		findings = kept
		if strings.HasPrefix(wfScope.Path, ".github/workflows/") && filesScanned > 0 {
			filesScanned = 1
		} else {
			filesScanned = 0
		}
	}

	// Nothing was linted and no history was read: instead of exiting 0 in
	// silence (the worst possible first-run experience), say what would
	// make the tool useful. Remote and --lint-only paths already errored.
	if !remoteLint && *baseFlag == "" && filesScanned == 0 && analysis == nil {
		fmt.Fprintf(os.Stderr, "nothing to scan: no workflows at %s and no GitHub repo to analyze.\n\n"+
			"  run gha-doctor inside a repo that uses GitHub Actions, or:\n"+
			"    gha-doctor --repo OWNER/NAME    scan any GitHub repo — no clone needed\n"+
			"    gha-doctor --dir PATH           point at a local checkout\n"+
			"    gha-doctor --help               all options\n", wfDir)
		os.Exit(1)
	}

	var score report.Score
	if wfScope == nil {
		score = report.ComputeScore(allFindings, filesScanned, analysis)
	} else if !*jsonOut && !*mdOut {
		// The health score grades the whole repo (hygiene across every
		// file, success across every workflow); a one-workflow sample
		// would be a different, unlabeled number.
		fmt.Fprintln(os.Stderr, "note: health score is whole-repo — not computed under --workflow")
	}
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
		if err := report.JSON(os.Stdout, findings, filesScanned, cfg, baseline, analysis, scorePtr, wins); err != nil {
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

	if *annotateOut {
		if *jsonOut || *sarifOut {
			// Machine-readable stdout must stay pure; skipping loudly beats
			// corrupting the stream (and lets the action pass --annotate
			// unconditionally).
			fmt.Fprintln(os.Stderr, "--annotate skipped: stdout is machine-readable (--json/--sarif); annotations would corrupt it")
		} else {
			report.Annotations(os.Stdout, []string{".", *dirFlag}, findings)
		}
	}

	if *promFlag != "" {
		if *promFlag == "-" && (*jsonOut || *sarifOut) {
			// Machine-readable stdout must stay pure (same rule as
			// --annotate): metrics appended after a JSON document would
			// corrupt it for every consumer.
			fmt.Fprintln(os.Stderr, "--prom - skipped: stdout is machine-readable (--json/--sarif); write to a file instead")
		} else {
			repoID := ""
			if o, n, err := resolveRepo(*repoFlag, *dirFlag); err == nil {
				repoID = o + "/" + n
			}
			writeProm(*promFlag, repoID, findings, filesScanned, analysis, scorePtr)
		}
	}

	if *htmlFlag != "" {
		var buf strings.Builder
		report.Markdown(&buf, findings, filesScanned, baseline, analysis, scorePtr, wins)
		// The page shell already carries the title; drop the markdown H1.
		md := strings.TrimPrefix(buf.String(), "## gha-doctor report\n\n")
		title := "gha-doctor report"
		if o, n, err := resolveRepo(*repoFlag, *dirFlag); err == nil {
			title = "gha-doctor — " + o + "/" + n
		}
		if wfScope != nil {
			title += " · " + wfScope.Name
		}
		meta := report.HTMLMeta{Title: title, Subtitle: htmlSubtitle(), Charts: report.Charts(analysis)}
		if scorePtr != nil {
			meta.Grade, meta.Points = score.Grade, score.Points
		}
		writeHTML(*htmlFlag, md, meta)
	}

	// Exit 2 is the CI gate. --fail-on picks the severity that trips it:
	// warning (the default), any finding at all, or never (report-only —
	// scheduled dashboard jobs shouldn't go red over known findings).
	switch failOn {
	case config.FailNever:
	case config.FailAny:
		if len(findings) > 0 {
			os.Exit(2)
		}
	default: // config.FailWarn
		for _, f := range findings {
			if f.Severity == lint.Warn {
				os.Exit(2)
			}
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

// baselineActionFiles collects action metadata files as they exist at ref,
// filtered by the same lint.IsActionPath scope as live discovery.
func baselineActionFiles(dir, ref string) ([]lint.NamedFile, error) {
	out, err := exec.Command("git", "-C", dir, "ls-tree", "-r", "--name-only", ref).Output()
	if err != nil {
		msg := gitStderr(err)
		return nil, fmt.Errorf("git ls-tree %s failed%s — is %q a fetched ref? (in shallow CI checkouts, fetch the base branch first: git fetch origin <branch>)", ref, msg, ref)
	}
	var files []lint.NamedFile
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || !lint.IsActionPath(line) {
			continue
		}
		if len(files) >= lint.MaxActionFiles {
			break
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

var sshRe = regexp.MustCompile(`^(?:ssh://)?git@([^:/]+)[:/]([^/]+)/(.+?)(?:\.git)?$`)
var httpsRe = regexp.MustCompile(`^https?://(?:[^/@]+@)?([^/:]+)(?::\d+)?/([^/]+)/(.+?)(?:\.git)?$`)

// resolveRepo determines owner/name from the flag or the git remote. The
// remote's host must match the host in effect (github.com, or GH_HOST /
// GITHUB_API_URL for GitHub Enterprise Server).
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
	host := api.Host()
	for _, re := range []*regexp.Regexp{sshRe, httpsRe} {
		if m := re.FindStringSubmatch(url); m != nil {
			if !strings.EqualFold(m[1], host) {
				return "", "", fmt.Errorf("remote %q is on %s but this run targets %s — pass --repo owner/name, or set GH_HOST=%s", url, m[1], host, strings.ToLower(m[1]))
			}
			return m[2], m[3], nil
		}
	}
	return "", "", fmt.Errorf("remote %q is not a %s URL", url, host)
}

// htmlSubtitle is the generated-by line under an HTML report's title.
func htmlSubtitle() string {
	return fmt.Sprintf("generated %s · gha-doctor %s",
		time.Now().UTC().Format("2006-01-02 15:04 UTC"), version)
}

// writeHTML renders md as a self-contained HTML page and writes it to path
// ("-" = stdout). Failure to write a requested report file is fatal, same
// as --badge.
func writeHTML(path, md string, meta report.HTMLMeta) {
	page := report.HTMLPage(md, meta)
	if path == "-" {
		os.Stdout.Write(page)
		return
	}
	if err := os.WriteFile(path, page, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "html:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "HTML report written to %s\n", path)
}

// writeBadge renders the health-score badge SVG to path.
// writeProm writes the Prometheus text-format export to path ("-" = stdout).
// Written whole to a buffer first so a mid-render error can't leave a
// truncated file where a textfile collector would scrape half-truths.
func writeProm(path, repoID string, findings []lint.Finding, filesScanned int, a *api.Analysis, sc *report.Score) {
	var buf strings.Builder
	if err := report.Prom(&buf, version, repoID, findings, filesScanned, a, sc, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "prom:", err)
		os.Exit(1)
	}
	if path == "-" {
		fmt.Print(buf.String())
		return
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "prom:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Prometheus metrics written to %s\n", path)
}

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

// unionRules merges two rule-ID lists (both already upper-cased), keeping
// the first list's order and appending IDs only the second list has.
func unionRules(a, b []string) []string {
	seen := map[string]bool{}
	out := a[:len(a):len(a)]
	for _, r := range a {
		seen[r] = true
	}
	for _, r := range b {
		if !seen[r] {
			seen[r] = true
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
