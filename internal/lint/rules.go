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
	ruleDoubleTrigger,     // D013
	ruleCronTopOfHour,     // D014
	ruleRetiredAction,     // D015
	ruleRetiredRunner,     // D016
	ruleDeprecatedCommand, // D018 (D017 is repo-level, not per-file)
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
			cadence := fmt.Sprintf("every %d minutes", mins)
			if mins == 1 {
				cadence = "every minute"
			}
			out = append(out, Finding{
				Rule: "D005", Severity: Warn, Line: cron.Line,
				Message: fmt.Sprintf("cron `%s` runs %s: ~%d runs/day of scheduled load", cron.Value, cadence, 1440/mins),
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
	if m == "*" {
		return 1, true // bare * minute = every minute
	}
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

// D013: `push` with no branch scoping plus `pull_request` runs the same
// commit twice for every PR opened from a branch in the same repo.
func ruleDoubleTrigger(w *Workflow) []Finding {
	trig, on := w.triggers()
	if _, pr := trig["pull_request"]; !pr {
		return nil
	}
	push, hasPush := trig["push"]
	if !hasPush {
		return nil
	}
	if push != nil && push.Kind == yaml.MappingNode {
		if branches := mapGet(push, "branches"); branches != nil {
			if !wildcardBranches(branches) {
				return nil // scoped to specific branches (e.g. main)
			}
		} else {
			if mapGet(push, "tags") != nil {
				return nil // tags-only push never fires for branch pushes
			}
			if mapGet(push, "branches-ignore") != nil {
				return nil // branch scoping is deliberate; assume PR branches are excluded
			}
		}
	}
	line := 1
	if push != nil && push.Line > 0 {
		line = push.Line
	} else if on != nil {
		line = on.Line
	}
	return []Finding{{
		Rule: "D013", Severity: Warn, Line: line,
		Message: "triggers on both `push` (all branches) and `pull_request`: every commit to a PR from this repo runs CI twice",
		Advice:  "scope push to the default branch — push: { branches: [main] } — pull_request already covers PR branches",
	}}
}

// wildcardBranches reports whether a `branches:` filter is effectively
// unscoped (contains a bare * or ** entry).
func wildcardBranches(branches *yaml.Node) bool {
	check := func(v string) bool { return v == "*" || v == "**" }
	switch branches.Kind {
	case yaml.ScalarNode:
		return check(branches.Value)
	case yaml.SequenceNode:
		for _, c := range branches.Content {
			if check(c.Value) {
				return true
			}
		}
	}
	return false
}

// D014: crons that fire at minute 0 land in GitHub's peak-load window,
// where scheduled runs are the most delayed and most often dropped.
func ruleCronTopOfHour(w *Workflow) []Finding {
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
		fields := strings.Fields(cron.Value)
		if len(fields) == 5 && fields[0] == "0" {
			out = append(out, Finding{
				Rule: "D014", Severity: Info, Line: cron.Line,
				Message: fmt.Sprintf("cron `%s` fires at minute 0 — the peak-load window where GitHub delays and sometimes skips scheduled runs", cron.Value),
				Advice:  "pick an arbitrary non-zero minute (e.g. `23 4 * * *`); off-peak schedules start closer to on-time",
			})
		}
	}
	return out
}

// ---- D015: retired action versions ----

// retiredAction describes an action whose old majors GitHub has shut down:
// steps that still reference them fail at runtime, every time.
type retiredAction struct {
	repos  []string // action repo (and subpath variants) to match
	majors map[int]bool
	when   string // human-readable shutdown date
	fix    string // recommended version
}

var retiredActions = []retiredAction{
	{
		repos:  []string{"actions/upload-artifact", "actions/download-artifact"},
		majors: map[int]bool{1: true, 2: true, 3: true},
		when:   "January 30, 2025",
		fix:    "v4",
	},
	{
		repos:  []string{"actions/cache", "actions/cache/restore", "actions/cache/save"},
		majors: map[int]bool{1: true, 2: true},
		when:   "March 1, 2025",
		fix:    "v4",
	},
}

// retiredActionFor returns the retiredAction entry matching an action name
// (case-insensitively, as GitHub resolves repos), or nil. Both the rule and
// the fixer resolve through this so their notion of "retired" can't drift:
// the fuzzer caught exactly that once — the fix bumping `actions/cache@0`
// (major 0, never a retired major) that the rule correctly ignored.
func retiredActionFor(name string) *retiredAction {
	for i := range retiredActions {
		for _, repo := range retiredActions[i].repos {
			if strings.EqualFold(name, repo) {
				return &retiredActions[i]
			}
		}
	}
	return nil
}

// refMajor extracts the major version from a `uses:` ref like "v3",
// "v3.1.2", or "3". Branch names and commit SHAs return -1: a pinned SHA
// may well be a retired build, but the ref alone can't prove it, and
// gha-doctor doesn't report what it can't verify.
func refMajor(ref string) int {
	ref = strings.TrimPrefix(ref, "v")
	dot := strings.IndexByte(ref, '.')
	if dot >= 0 {
		ref = ref[:dot]
	}
	n, err := strconv.Atoi(ref)
	if err != nil {
		return -1
	}
	return n
}

// D015: actions pinned to a version GitHub has shut down. Unlike every
// other rule, this isn't about waste — these steps hard-fail at runtime.
func ruleRetiredAction(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			u := mapGet(step, "uses")
			if u == nil {
				return
			}
			at := strings.IndexByte(u.Value, '@')
			if at < 0 {
				return
			}
			name, ref := u.Value[:at], u.Value[at+1:]
			ra := retiredActionFor(name)
			if ra == nil {
				return
			}
			if m := refMajor(ref); m >= 0 && ra.majors[m] {
				out = append(out, Finding{
					Rule: "D015", Severity: Warn, Line: u.Line,
					Message: fmt.Sprintf("`%s` was shut down by GitHub on %s — this step fails at runtime", u.Value, ra.when),
					Advice:  fmt.Sprintf("update to %s@%s", name, ra.fix),
				})
			}
		})
	})
	return out
}

// ---- D016: retired runner labels ----

// retiredRunners maps hosted runner labels GitHub has fully retired to
// their retirement date and the labels GitHub recommends instead.
var retiredRunners = map[string]struct {
	when    string
	instead string
}{
	"ubuntu-16.04":    {"September 2021", "ubuntu-22.04 or ubuntu-24.04"},
	"ubuntu-18.04":    {"April 2023", "ubuntu-22.04 or ubuntu-24.04"},
	"ubuntu-20.04":    {"April 15, 2025", "ubuntu-22.04 or ubuntu-24.04"},
	"windows-2016":    {"June 2022", "windows-2022 or windows-2025"},
	"windows-2019":    {"June 30, 2025", "windows-2022 or windows-2025"},
	"macos-10.15":     {"September 2022", "macos-14 or macos-15"},
	"macos-11":        {"June 2024", "macos-14 or macos-15"},
	"macos-12":        {"December 3, 2024", "macos-14 or macos-15"},
	"macos-12-xl":     {"December 3, 2024", "macos-14 or macos-15"},
	"macos-13":        {"December 4, 2025", "macos-14 or macos-15"},
	"macos-13-xl":     {"December 4, 2025", "macos-14 or macos-15"},
	"macos-13-large":  {"December 4, 2025", "macos-14-large or macos-15-large"},
	"macos-13-xlarge": {"December 4, 2025", "macos-14-xlarge or macos-15-xlarge"},
}

// D016: jobs that ask for a hosted runner label GitHub has retired.
// These jobs fail immediately (or hang until the 24h queue timeout) —
// the workflow cannot succeed. Checks scalar runs-on, label lists, and
// `${{ matrix.KEY }}` indirection through strategy.matrix values and
// include entries.
func ruleRetiredRunner(w *Workflow) []Finding {
	var out []Finding
	flag := func(n *yaml.Node, jobID string) {
		info, ok := retiredRunners[strings.ToLower(strings.TrimSpace(n.Value))]
		if !ok {
			return
		}
		out = append(out, Finding{
			Rule: "D016", Severity: Warn, Line: n.Line,
			Message: fmt.Sprintf("runner label `%s` (job `%s`) was retired by GitHub on %s — jobs requesting it cannot run", n.Value, jobID, info.when),
			Advice:  "switch to " + info.instead,
		})
	}
	w.jobs(func(id string, key, job *yaml.Node) {
		ro := mapGet(job, "runs-on")
		if ro == nil {
			return
		}
		switch ro.Kind {
		case yaml.ScalarNode:
			if k := matrixKeyRef(ro.Value); k != "" {
				matrixValues(job, k, func(v *yaml.Node) { flag(v, id) })
			} else {
				flag(ro, id)
			}
		case yaml.SequenceNode:
			for _, item := range ro.Content {
				if item.Kind == yaml.ScalarNode {
					flag(item, id)
				}
			}
		}
	})
	return out
}

// matrixKeyRef extracts KEY from a runs-on value that is exactly
// `${{ matrix.KEY }}`; anything else returns "".
func matrixKeyRef(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "${{") || !strings.HasSuffix(v, "}}") {
		return ""
	}
	inner := strings.TrimSpace(v[3 : len(v)-2])
	if !strings.HasPrefix(inner, "matrix.") {
		return ""
	}
	key := strings.TrimPrefix(inner, "matrix.")
	if strings.ContainsAny(key, " .([|&!<>=") {
		return "" // an expression, not a plain key reference
	}
	return key
}

// matrixValues yields every scalar value the matrix can assign to key:
// the axis list itself plus any include entries that set it.
func matrixValues(job *yaml.Node, key string, fn func(*yaml.Node)) {
	strat := mapGet(job, "strategy")
	matrix := mapGet(strat, "matrix")
	if matrix == nil {
		return
	}
	if axis := mapGet(matrix, key); axis != nil && axis.Kind == yaml.SequenceNode {
		for _, v := range axis.Content {
			if v.Kind == yaml.ScalarNode {
				fn(v)
			}
		}
	}
	if inc := mapGet(matrix, "include"); inc != nil && inc.Kind == yaml.SequenceNode {
		for _, entry := range inc.Content {
			if v := mapGet(entry, key); v != nil && v.Kind == yaml.ScalarNode {
				fn(v)
			}
		}
	}
}

// ---- D018: deprecated workflow commands ----

// deprecatedCommand describes one stdout workflow command GitHub has
// deprecated or disabled, and the environment file that replaces it.
// The rule and the fixer both read this table, so they cannot drift.
type deprecatedCommand struct {
	name     string // command as it appears after "::"
	takesKey bool   // ::cmd name=KEY::VALUE vs ::cmd::VALUE
	target   string // replacement environment file variable
	status   string // what GitHub did to it, for the message
	broken   bool   // true = errors at runtime today, not just a warning
}

var deprecatedCommands = []deprecatedCommand{
	{"set-env", true, "GITHUB_ENV", "disabled by GitHub in November 2020", true},
	{"add-path", false, "GITHUB_PATH", "disabled by GitHub in November 2020", true},
	{"set-output", true, "GITHUB_OUTPUT", "deprecated by GitHub in October 2022", false},
	{"save-state", true, "GITHUB_STATE", "deprecated by GitHub in October 2022", false},
}

// deprecatedCmdsIn reports which deprecated workflow commands appear in a
// run script, ignoring comment lines. Shared by rule D018 and its fixer.
func deprecatedCmdsIn(script string) []deprecatedCommand {
	var out []deprecatedCommand
	seen := map[string]bool{}
	for _, line := range strings.Split(script, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		for _, dc := range deprecatedCommands {
			if !seen[dc.name] && strings.Contains(t, "::"+dc.name) {
				seen[dc.name] = true
				out = append(out, dc)
			}
		}
	}
	return out
}

// D018: run steps that emit deprecated stdout workflow commands.
// ::set-env and ::add-path have been disabled since November 2020 — the
// step errors at runtime. ::set-output and ::save-state still work but
// print a deprecation warning on every run, and GitHub has announced
// their removal. The environment-file replacements are mechanical.
func ruleDeprecatedCommand(w *Workflow) []Finding {
	var out []Finding
	w.jobs(func(id string, key, job *yaml.Node) {
		jobSteps(job, func(step *yaml.Node) {
			run := mapGet(step, "run")
			if run == nil {
				return
			}
			for _, dc := range deprecatedCmdsIn(run.Value) {
				effect := "GitHub prints a deprecation warning on every run and has announced its removal"
				if dc.broken {
					effect = "the step errors at runtime"
				}
				example := fmt.Sprintf("echo \"name=value\" >> \"$%s\"", dc.target)
				if !dc.takesKey {
					example = fmt.Sprintf("echo \"/extra/path\" >> \"$%s\"", dc.target)
				}
				out = append(out, Finding{
					Rule: "D018", Severity: Warn, Line: run.Line,
					Message: fmt.Sprintf("run step in job `%s` uses `::%s`, %s — %s", id, dc.name, dc.status, effect),
					Advice:  fmt.Sprintf("write to the environment file instead: %s (--fix rewrites simple echo lines)", example),
				})
			}
		})
	})
	return out
}
