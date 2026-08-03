package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/mcp"
)

// The MCP surface is read-only by design: every tool reports or previews,
// none writes to the repository. Fixes stay a deliberate human (or
// explicitly-scripted) `--fix` away.

const mcpInstructions = "gha-doctor diagnoses GitHub Actions CI: static workflow lint " +
	"(21 rules), run-history analysis (failure/flaky/waste/cost, named flaky tests, " +
	"health score), single-run deep dives, and fix previews. All tools are read-only. " +
	"History tools call the GitHub API: set GITHUB_TOKEN (or be logged in via the gh CLI) " +
	"to avoid the 60-requests/hour unauthenticated limit; reading logs (flaky_logs, " +
	"log_tail) always needs a token. Static lint of a local directory works offline. " +
	"Dollar figures are estimated compute value at public hosted-runner rates, not an invoice."

var mcpRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var mcpRuleRe = regexp.MustCompile(`^[Dd][0-9]{3}$`)
var mcpOrgRe = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// mcpOutputCap keeps a runaway report from flooding the client.
const mcpOutputCap = 1 << 20

func runMCP() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp: cannot locate own executable:", err)
		return 1
	}
	srv := &mcp.Server{
		Name:         "gha-doctor",
		Title:        "gha-doctor — GitHub Actions CI doctor",
		Version:      version,
		Instructions: mcpInstructions,
		Tools:        mcpTools(exe),
	}
	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		return 1
	}
	return 0
}

func mcpTools(exe string) []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "analyze_repo",
			Description: "Full CI health report for a GitHub repository: static workflow lint, " +
				"run-history analysis (success/failure rates, flaky jobs, retry waste, superseded PR runs, " +
				"queue times, duration trends, zombie crons, PR feedback time), cache and artifact checkups, " +
				"a 0-100 health score, and a ranked, dollar-quantified top-wins list. " +
				"Makes ~100+ GitHub API calls and typically takes 10-30 seconds. " +
				"Optionally set flaky_logs to also read N flaky-failure job logs and name the flaky tests " +
				"(needs a token), or cache_logs to measure the real cache hit rate from N job logs.",
			InputSchema: mcpSchema(map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository as owner/name, e.g. cli/cli",
				},
				"runs": map[string]any{
					"type": "integer", "minimum": 10, "maximum": 1000,
					"description": "Number of recent runs to sample (default 100)",
				},
				"workflow": map[string]any{
					"type":        "string",
					"description": "Scope the history analysis to one workflow (file name like ci.yml, or display name); no health score in scoped mode",
				},
				"flaky_logs": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 20,
					"description": "Read N flaky-failure job logs to name the flaky tests (1 API request per log; needs a token)",
				},
				"cache_logs": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 50,
					"description": "Sample N job logs to measure the real cache hit/miss rate (1 API request per log; needs a token)",
				},
			}, "repo"),
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				repo, errText := mcpStr(args, "repo", mcpRepoRe, "owner/name")
				if errText != "" {
					return errText, true
				}
				cargs := []string{"--md", "--repo", repo}
				if v, s, bad := mcpInt(args, "runs", 10, 1000); bad != "" {
					return bad, true
				} else if s {
					cargs = append(cargs, "--runs", fmt.Sprint(v))
				}
				if wf, ok := args["workflow"].(string); ok && wf != "" {
					cargs = append(cargs, "--workflow", wf)
				}
				if v, s, bad := mcpInt(args, "flaky_logs", 0, 20); bad != "" {
					return bad, true
				} else if s {
					cargs = append(cargs, "--flaky-logs", fmt.Sprint(v))
				}
				if v, s, bad := mcpInt(args, "cache_logs", 0, 50); bad != "" {
					return bad, true
				} else if s {
					cargs = append(cargs, "--cache-logs", fmt.Sprint(v))
				}
				return runSelf(ctx, exe, 5*time.Minute, cargs...)
			},
		},
		{
			Name: "lint_repo",
			Description: "Static lint of GitHub Actions workflows and action manifests only (no run history, " +
				"fast): 21 rules covering missing timeouts, no concurrency cancellation, dead action versions, " +
				"retired runner labels, deprecated workflow commands, unguarded crons, and more. " +
				"Pass repo to lint any GitHub repository via the API (no clone needed), or dir for a local " +
				"checkout (offline). Defaults to the current directory.",
			InputSchema: mcpSchema(map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository as owner/name to lint remotely (mutually exclusive with dir)",
				},
				"dir": map[string]any{
					"type":        "string",
					"description": "Local repository directory to lint (default \".\")",
				},
			}),
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				cargs, errText := mcpTarget(args, []string{"--md", "--lint-only"})
				if errText != "" {
					return errText, true
				}
				return runSelf(ctx, exe, 2*time.Minute, cargs...)
			},
		},
		{
			Name: "preview_fixes",
			Description: "Preview the exact unified diff that gha-doctor --fix would apply for auto-fixable " +
				"findings (10 of 21 rules), without writing anything. Pass repo for any GitHub repository " +
				"(no clone needed) or dir for a local checkout. To actually apply fixes, run " +
				"`gha-doctor --fix` in the repository — this MCP server never writes.",
			InputSchema: mcpSchema(map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository as owner/name (mutually exclusive with dir)",
				},
				"dir": map[string]any{
					"type":        "string",
					"description": "Local repository directory (default \".\")",
				},
			}),
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				cargs, errText := mcpTarget(args, []string{"--md", "--diff"})
				if errText != "" {
					return errText, true
				}
				return runSelf(ctx, exe, 2*time.Minute, cargs...)
			},
		},
		{
			Name: "run_deep_dive",
			Description: "Deep-dive one workflow run: why was it slow, or why did it fail? Job waterfall " +
				"(queue vs execution), every job and step compared against the workflow's own recent medians, " +
				"named step regressions; failed runs lead with the failing job and step, name the failing " +
				"tests (20+ test frameworks recognized), and inline the failing step's log tail (needs a token).",
			InputSchema: mcpSchema(map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository as owner/name",
				},
				"run": map[string]any{
					"type":        "string",
					"description": "Run ID, full run URL, or \"latest\" (default \"latest\")",
				},
				"log_tail": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 200,
					"description": "Lines of the failing step's log to include per failed job (default 20, 0 = off; needs a token)",
				},
			}, "repo"),
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				repo, errText := mcpStr(args, "repo", mcpRepoRe, "owner/name")
				if errText != "" {
					return errText, true
				}
				run := "latest"
				if v, ok := args["run"].(string); ok && v != "" {
					run = v
				}
				cargs := []string{"--md", "--repo", repo, "--run", run}
				if v, s, bad := mcpInt(args, "log_tail", 0, 200); bad != "" {
					return bad, true
				} else if s {
					cargs = append(cargs, "--log-tail", fmt.Sprint(v))
				}
				return runSelf(ctx, exe, 3*time.Minute, cargs...)
			},
		},
		{
			Name: "org_overview",
			Description: "Fleet triage across an organization's (or user's) most recently pushed repositories: " +
				"per-repo run counts, failure rates, median duration, compute minutes, and last-run age, " +
				"one API call per repo. Useful for finding which repository's CI to look at first.",
			InputSchema: mcpSchema(map[string]any{
				"org": map[string]any{
					"type":        "string",
					"description": "GitHub organization or user login",
				},
				"max_repos": map[string]any{
					"type": "integer", "minimum": 1, "maximum": 50,
					"description": "Max repositories to scan, most recently pushed first (default 20)",
				},
			}, "org"),
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				org, errText := mcpStr(args, "org", mcpOrgRe, "a GitHub login")
				if errText != "" {
					return errText, true
				}
				cargs := []string{"--md", "--org", org}
				if v, s, bad := mcpInt(args, "max_repos", 1, 50); bad != "" {
					return bad, true
				} else if s {
					cargs = append(cargs, "--max-repos", fmt.Sprint(v))
				}
				return runSelf(ctx, exe, 5*time.Minute, cargs...)
			},
		},
		{
			Name: "explain_rule",
			Description: "Print the full documentation for one lint rule: what it flags, why it matters, " +
				"how to fix it, and how to suppress it. Rule IDs look like D001; every finding cites one.",
			InputSchema: mcpSchema(map[string]any{
				"rule": map[string]any{
					"type": "string", "pattern": "^[Dd][0-9]{3}$",
					"description": "Rule ID, e.g. D001",
				},
			}, "rule"),
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				rule, errText := mcpStr(args, "rule", mcpRuleRe, "a rule ID like D001")
				if errText != "" {
					return errText, true
				}
				return runSelf(ctx, exe, 30*time.Second, "--explain", strings.ToUpper(rule))
			},
		},
	}
}

// mcpTarget builds the arg list for tools that accept either a remote repo
// or a local dir (mutually exclusive; default is the current directory).
func mcpTarget(args map[string]any, base []string) ([]string, string) {
	repo, hasRepo := args["repo"].(string)
	dir, hasDir := args["dir"].(string)
	switch {
	case hasRepo && repo != "" && hasDir && dir != "":
		return nil, "pass either repo or dir, not both"
	case hasRepo && repo != "":
		if !mcpRepoRe.MatchString(repo) {
			return nil, "repo must look like owner/name"
		}
		return append(base, "--repo", repo), ""
	case hasDir && dir != "":
		return append(base, "--dir", dir), ""
	default:
		return append(base, "--dir", "."), ""
	}
}

func mcpSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func mcpStr(args map[string]any, key string, re *regexp.Regexp, want string) (string, string) {
	v, ok := args[key].(string)
	if !ok || v == "" {
		return "", key + " is required and must be " + want
	}
	if !re.MatchString(v) {
		return "", key + " must be " + want
	}
	return v, ""
}

// mcpInt reads an optional integer argument. Returns (value, wasSet, errText).
func mcpInt(args map[string]any, key string, min, max int) (int, bool, string) {
	raw, ok := args[key]
	if !ok {
		return 0, false, ""
	}
	f, ok := raw.(float64)
	if !ok || f != math.Trunc(f) {
		return 0, false, fmt.Sprintf("%s must be an integer between %d and %d", key, min, max)
	}
	v := int(f)
	if v < min || v > max {
		return 0, false, fmt.Sprintf("%s must be between %d and %d", key, min, max)
	}
	return v, true, ""
}

// runSelf executes this same binary with the given args and maps the outcome
// to an MCP tool result. Exit codes 0 (clean) and 2 (findings) are both
// successful reports; anything else is a tool error carrying stderr.
func runSelf(ctx context.Context, exe string, timeout time.Duration, args ...string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, exe, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()

	if cctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("timed out after %s", timeout), true
	}
	if cctx.Err() == context.Canceled {
		return "cancelled", true
	}
	stderr := strings.TrimSpace(errb.String())
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
			text := stderr
			if text == "" {
				text = err.Error()
			}
			if o := strings.TrimSpace(out.String()); o != "" {
				text = o + "\n\n" + text
			}
			return mcpCap(text), true
		}
	}
	text := out.String()
	if stderr != "" {
		// The CLI's honest notes (config in effect, sampling caveats,
		// missing-token hints) land on stderr; they are part of the story.
		text += "\n---\nNotes:\n" + stderr + "\n"
	}
	return mcpCap(text), false
}

func mcpCap(s string) string {
	if len(s) <= mcpOutputCap {
		return s
	}
	return s[:mcpOutputCap] + "\n\n[output truncated at 1 MiB]"
}
