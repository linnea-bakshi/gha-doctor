package lint

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// AllRules is the registry of built-in rules, in ID order.
var AllRules = []Rule{
	ruleConcurrency,       // D001
	ruleTimeout,           // D002
	ruleSetupCache,        // D003
	ruleFetchDepth,        // D004
	ruleCronFrequency,     // D005
	ruleExpensiveRunner,   // D006
	ruleDockerBuildCache,  // D007
	ruleCacheRestoreKeys,  // D008
	ruleContinueOnError,   // D009
	ruleArtifactRetention, // D010
	ruleMatrixSize,        // D011
	ruleNpmInstall,        // D012
}

// D001: workflows triggered by pull_request/push should define concurrency
// with cancel-in-progress so superseded runs don't burn minutes.
func ruleConcurrency(w *Workflow) []Finding {
	trig, on := w.triggers()
	_, pr := trig["pull_request"]
	_, prt := trig["pull_request_target"]
	if !pr && !prt {
		return nil
	}
	conc := mapGet(w.Doc, "concurrency")
	if conc == nil {
		line := 1
		if on != nil {
			line = on.Line
		}
		return []Finding{{
			Rule: "D001", Severity: Warn, Line: line,
			Message: "pull_request workflow has no `concurrency` group: pushing new commits leaves superseded runs burning minutes",
			Advice:  "add: concurrency: { group: ${{ github.workflow }}-${{ github.ref }}, cancel-in-progress: true }",
		}}
	}
	if conc.Kind == yaml.MappingNode {
		cip := mapGet(conc, "cancel-in-progress")
		if cip == nil || cip.Value == "false" {
			return []Finding{{
				Rule: "D001", Severity: Info, Line: conc.Line,
				Message: "concurrency group defined but `cancel-in-progress` is not enabled; superseded PR runs will queue instead of cancel",
				Advice:  "set cancel-in-progress: true (or an expression that enables it for PRs)",
			}}
		}
	}
	return nil
}

// D002: jobs without timeout-minutes default to 360 minutes; a hung job can
// burn 6 hours of runner time.
func ruleTimeout(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		if mapGet(job, "uses") != nil {
			return // reusable workflow call; timeout set in callee
		}
		if mapGet(job, "timeout-minutes") == nil {
			out = append(out, Finding{
				Rule: "D002", Severity: Warn, Line: key.Line,
				Message: fmt.Sprintf("job `%s` has no timeout-minutes (default is 360): one hung step can burn 6 hours of runner time", id),
				Advice:  "set timeout-minutes to a little above the job's normal duration, e.g. timeout-minutes: 15",
			})
		}
	})
	return out
}

// D003: setup-node/python/java don't cache package downloads unless you
// opt in with the `cache:` input.
func ruleSetupCache(w *Workflow) []Finding {
	targets := map[string]string{
		"actions/setup-node":   "cache: npm (or yarn/pnpm)",
		"actions/setup-python": "cache: pip (or pipenv/poetry)",
		"actions/setup-java":   "cache: maven (or gradle/sbt)",
	}
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			for action, hint := range targets {
				if !usesAction(step, action) {
					continue
				}
				with := mapGet(step, "with")
				if mapGet(with, "cache") == nil {
					u := mapGet(step, "uses")
					out = append(out, Finding{
						Rule: "D003", Severity: Warn, Line: u.Line,
						Message: fmt.Sprintf("%s without `cache:` input re-downloads dependencies on every run", action),
						Advice:  "add `with: { " + hint + " }` to cache the package manager's downloads",
					})
				}
			}
		})
	})
	return out
}

// D004: fetch-depth: 0 clones full history; often unnecessary and slow on
// large repos.
func ruleFetchDepth(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			if !usesAction(step, "actions/checkout") {
				return
			}
			with := mapGet(step, "with")
			fd := mapGet(with, "fetch-depth")
			if fd != nil && fd.Value == "0" {
				out = append(out, Finding{
					Rule: "D004", Severity: Info, Line: fd.Line,
					Message: "checkout with fetch-depth: 0 clones full history; on large repos this dominates job time",
					Advice:  "only fetch full history if the job truly needs it (e.g. changelog/semantic-release); otherwise drop it",
				})
			}
		})
	})
	return out
}

// D005: cron schedules more frequent than every 15 minutes burn minutes fast
// and are throttled by GitHub anyway.
func ruleCronFrequency(w *Workflow) []Finding {
	trig, _ := w.triggers()
	sched, ok := trig["schedule"]
	if !ok || sched == nil || sched.Kind != yaml.SequenceNode {
		return nil
	}
	var out []Finding
	for _, item := range sched.Content {
		cron := mapGet(item, "cron")
		if cron == nil {
			continue
		}
		if mins, every := cronEveryNMinutes(cron.Value); every && mins < 15 {
			out = append(out, Finding{
				Rule: "D005", Severity: Warn, Line: cron.Line,
				Message: fmt.Sprintf("cron `%s` runs every %d minutes: ~%d runs/day of scheduled load", cron.Value, mins, 1440/mins),
				Advice:  "GitHub delays/drops high-frequency schedules under load; consider >=15 min or event-driven triggers",
			})
		}
	}
	return out
}

// cronEveryNMinutes parses the minute field for the */N pattern.
func cronEveryNMinutes(expr string) (int, bool) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return 0, false
	}
	m := fields[0]
	if strings.HasPrefix(m, "*/") {
		n, err := strconv.Atoi(m[2:])
		if err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}

// D006: macOS runners bill at 10x, Windows at 2x the Linux rate.
func ruleExpensiveRunner(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		ro := mapGet(job, "runs-on")
		if ro == nil || ro.Kind != yaml.ScalarNode {
			return
		}
		v := strings.ToLower(ro.Value)
		var mult string
		switch {
		case strings.HasPrefix(v, "macos"):
			mult = "10x"
		case strings.HasPrefix(v, "windows"):
			mult = "2x"
		default:
			return
		}
		trig, _ := w.triggers()
		if _, ok := trig["schedule"]; !ok {
			if _, ok := trig["push"]; !ok {
				return
			}
		}
		out = append(out, Finding{
			Rule: "D006", Severity: Info, Line: ro.Line,
			Message: fmt.Sprintf("job `%s` runs on %s (%s billing multiplier) on every push/schedule", id, ro.Value, mult),
			Advice:  "run expensive runners on a filtered trigger (release tags, paths) or behind a Linux smoke test",
		})
	})
	return out
}

// D007: docker/build-push-action without cache-from rebuilds all layers
// every run.
func ruleDockerBuildCache(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			if !usesAction(step, "docker/build-push-action") {
				return
			}
			with := mapGet(step, "with")
			if mapGet(with, "cache-from") == nil {
				u := mapGet(step, "uses")
				out = append(out, Finding{
					Rule: "D007", Severity: Warn, Line: u.Line,
					Message: "docker build without `cache-from`: every run rebuilds all layers from scratch",
					Advice:  "add with: { cache-from: type=gha, cache-to: type=gha,mode=max }",
				})
			}
		})
	})
	return out
}

// D008: actions/cache without restore-keys means any key miss is a full
// cold start.
func ruleCacheRestoreKeys(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			if !usesAction(step, "actions/cache") {
				return
			}
			with := mapGet(step, "with")
			if with != nil && mapGet(with, "restore-keys") == nil {
				u := mapGet(step, "uses")
				out = append(out, Finding{
					Rule: "D008", Severity: Info, Line: u.Line,
					Message: "actions/cache without `restore-keys`: a lockfile change means a fully cold cache",
					Advice:  "add restore-keys with a key prefix so stale-but-close caches are restored and updated",
				})
			}
		})
	})
	return out
}

// D009: continue-on-error at job level silently masks failures.
func ruleContinueOnError(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		coe := mapGet(job, "continue-on-error")
		if coe != nil && coe.Value == "true" {
			out = append(out, Finding{
				Rule: "D009", Severity: Info, Line: coe.Line,
				Message: fmt.Sprintf("job `%s` has continue-on-error: true — failures are green-washed and easy to miss", id),
				Advice:  "prefer marking known-flaky matrix legs via `strategy.matrix` + `exclude`, or surface a status check",
			})
		}
	})
	return out
}

// D010: uploaded artifacts default to 90-day retention, which accumulates
// storage cost.
func ruleArtifactRetention(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			if !usesAction(step, "actions/upload-artifact") {
				return
			}
			with := mapGet(step, "with")
			if mapGet(with, "retention-days") == nil {
				u := mapGet(step, "uses")
				out = append(out, Finding{
					Rule: "D010", Severity: Info, Line: u.Line,
					Message: "artifact uploaded with default 90-day retention; storage accumulates on busy repos",
					Advice:  "set with: { retention-days: 7 } (or what you actually need)",
				})
			}
		})
	})
	return out
}

// D011: large static matrices multiply every push into many jobs.
func ruleMatrixSize(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		strat := mapGet(job, "strategy")
		matrix := mapGet(strat, "matrix")
		if matrix == nil || matrix.Kind != yaml.MappingNode {
			return
		}
		combos := 1
		for i := 0; i+1 < len(matrix.Content); i += 2 {
			k, v := matrix.Content[i].Value, matrix.Content[i+1]
			if k == "include" || k == "exclude" {
				continue
			}
			if v.Kind == yaml.SequenceNode {
				combos *= len(v.Content)
			}
		}
		if combos >= 20 {
			out = append(out, Finding{
				Rule: "D011", Severity: Warn, Line: matrix.Line,
				Message: fmt.Sprintf("job `%s` matrix expands to %d jobs per trigger", id, combos),
				Advice:  "consider running the full matrix only on main/release and a reduced matrix on PRs",
			})
		}
	})
	return out
}

// D012: `npm install` in CI is slower and less reproducible than `npm ci`.
func ruleNpmInstall(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			run := mapGet(step, "run")
			if run == nil {
				return
			}
			for _, line := range strings.Split(run.Value, "\n") {
				l := strings.TrimSpace(line)
				if l == "npm install" || strings.HasPrefix(l, "npm install ") &&
					!strings.Contains(l, "-g") && !strings.Contains(l, "--global") {
					out = append(out, Finding{
						Rule: "D012", Severity: Info, Line: run.Line,
						Message: "`npm install` in CI: slower than `npm ci` and can drift from the lockfile",
						Advice:  "use `npm ci` for clean, reproducible, faster installs in CI",
					})
					return
				}
			}
		})
	})
	return out
}
