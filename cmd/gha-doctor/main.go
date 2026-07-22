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
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
	"github.com/linnea-bakshi/gha-doctor/internal/report"
)

var version = "dev"

func main() {
	var (
		repoFlag    = flag.String("repo", "", "owner/name to analyze (default: detect from git remote)")
		runsFlag    = flag.Int("runs", 100, "number of recent runs to sample for history analysis")
		lintOnly    = flag.Bool("lint-only", false, "only run static workflow checks (no API calls)")
		jsonOut     = flag.Bool("json", false, "output JSON")
		mdOut       = flag.Bool("md", false, "output Markdown (for pasting into an issue)")
		dirFlag     = flag.String("dir", ".", "repository directory to scan")
		versionFlag = flag.Bool("version", false, "print version")
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

	// Static lint
	wfDir := filepath.Join(*dirFlag, ".github", "workflows")
	var findings []lint.Finding
	filesScanned := 0
	if fi, err := os.Stat(wfDir); err == nil && fi.IsDir() {
		var err error
		findings, filesScanned, err = lint.LintDir(wfDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error scanning workflows:", err)
			os.Exit(1)
		}
	} else if *lintOnly {
		fmt.Fprintf(os.Stderr, "no workflows found at %s\n", wfDir)
		os.Exit(1)
	}

	// History analysis
	var analysis *api.Analysis
	if !*lintOnly {
		owner, name, err := resolveRepo(*repoFlag, *dirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot determine repo (%v); running static checks only\n", err)
		} else {
			c := api.NewClient()
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

	switch {
	case *jsonOut:
		if err := report.JSON(os.Stdout, findings, analysis); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case *mdOut:
		report.Markdown(os.Stdout, findings, filesScanned, analysis)
	default:
		s := report.AutoStyle()
		report.Findings(os.Stdout, s, findings, filesScanned)
		if analysis != nil {
			report.Analysis(os.Stdout, s, analysis)
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
