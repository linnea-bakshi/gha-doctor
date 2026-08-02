package lint

import (
	"strings"
	"testing"
)

func lintYAML(t *testing.T, y string) []Finding {
	t.Helper()
	fs, err := LintBytes("test.yml", []byte(y))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return fs
}

func rules(fs []Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Rule]++
	}
	return m
}

const cleanWorkflow = `
name: CI
on:
  pull_request:
  push: {branches: [main]}
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: {cache: npm}
      - run: npm ci
      - run: npm test
`

func TestCleanWorkflowHasNoFindings(t *testing.T) {
	fs := lintYAML(t, cleanWorkflow)
	if len(fs) != 0 {
		t.Fatalf("expected no findings, got %v", fs)
	}
}

func TestD001MissingConcurrency(t *testing.T) {
	y := `
on: pull_request
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	fs := lintYAML(t, y)
	if rules(fs)["D001"] != 1 {
		t.Fatalf("expected D001, got %v", fs)
	}
}

func TestD001CancelInProgressFalse(t *testing.T) {
	y := `
on: [pull_request]
concurrency:
  group: g
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	fs := lintYAML(t, y)
	found := false
	for _, f := range fs {
		if f.Rule == "D001" && f.Severity == Info {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected D001 info, got %v", fs)
	}
}

func TestD002MissingTimeout(t *testing.T) {
	y := `
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps: [{run: make}]
  call-reusable:
    uses: ./.github/workflows/other.yml
`
	fs := lintYAML(t, y)
	if rules(fs)["D002"] != 1 { // reusable call exempt
		t.Fatalf("expected exactly 1 D002, got %v", fs)
	}
}

func TestD003SetupWithoutCache(t *testing.T) {
	y := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/setup-python@v5
      - uses: actions/setup-node@v4
        with: {node-version: 20}
      - uses: actions/setup-go@v5
`
	fs := lintYAML(t, y)
	if rules(fs)["D003"] != 2 { // setup-go caches by default, not flagged
		t.Fatalf("expected 2 D003, got %v", fs)
	}
}

func TestD004FetchDepthZero(t *testing.T) {
	y := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v4
        with: {fetch-depth: 0}
`
	if rules(lintYAML(t, y))["D004"] != 1 {
		t.Fatal("expected D004")
	}
}

func TestD005HighFrequencyCron(t *testing.T) {
	y := `
on:
  schedule:
    - cron: "*/5 * * * *"
    - cron: "0 3 * * *"
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	fs := lintYAML(t, y)
	if rules(fs)["D005"] != 1 {
		t.Fatalf("expected 1 D005, got %v", fs)
	}
}

func TestD006MacOSOnPush(t *testing.T) {
	y := `
on: push
jobs:
  a:
    runs-on: macos-14
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	if rules(lintYAML(t, y))["D006"] != 1 {
		t.Fatal("expected D006")
	}
}

func TestD007DockerNoCache(t *testing.T) {
	y := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: docker/build-push-action@v6
        with: {push: true}
`
	if rules(lintYAML(t, y))["D007"] != 1 {
		t.Fatal("expected D007")
	}
}

func TestD011LargeMatrix(t *testing.T) {
	y := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    strategy:
      matrix:
        os: [a, b, c, d, e]
        ver: [1, 2, 3, 4]
    steps: [{run: echo hi}]
`
	if rules(lintYAML(t, y))["D011"] != 1 {
		t.Fatal("expected D011 for 20-job matrix")
	}
}

func TestD012NpmInstall(t *testing.T) {
	y := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: |
          npm install
          npm test
      - run: npm install -g some-tool
`
	if rules(lintYAML(t, y))["D012"] != 1 {
		t.Fatal("expected exactly 1 D012 (global install exempt)")
	}
}

func TestD012PackageInstallsAreNotFindings(t *testing.T) {
	// `npm install <pkg|tarball|dir>` installs a specific package — npm ci
	// is not a substitute, so these must not be flagged at all. Flags-only
	// installs are still lockfile-driven dependency installs and stay
	// findings.
	y := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: npm install typescript@5
      - run: npm install ./gha-doctor-0.42.2.tgz
      - run: npm install --no-audit --no-fund ../local-dir
`
	if got := rules(lintYAML(t, y))["D012"]; got != 0 {
		t.Fatalf("package installs must not be D012 findings, got %d", got)
	}
	y2 := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: npm install --legacy-peer-deps
`
	if got := rules(lintYAML(t, y2))["D012"]; got != 1 {
		t.Fatalf("flags-only install is still a D012 finding, got %d", got)
	}
}

func TestTriggersBareOnParsing(t *testing.T) {
	// `on:` is YAML-1.1-truthy; ensure we still find triggers.
	for _, y := range []string{"on: push\njobs: {}", "on: [push, pull_request]\njobs: {}"} {
		var w = mustParse(t, y)
		trig, _ := w.triggers()
		if _, ok := trig["push"]; !ok {
			t.Fatalf("push trigger not detected in %q", y)
		}
	}
}

func mustParse(t *testing.T, y string) *Workflow {
	t.Helper()
	fs, err := LintBytes("t.yml", []byte(y))
	_ = fs
	if err != nil {
		t.Fatal(err)
	}
	// re-parse for direct access
	var root struct{}
	_ = root
	w, err := parseForTest(y)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func parseForTest(y string) (*Workflow, error) {
	var n = new(Workflow)
	var err error
	n, err = parseWorkflow("t.yml", []byte(y))
	return n, err
}

func TestCronParser(t *testing.T) {
	cases := []struct {
		expr  string
		mins  int
		every bool
	}{
		{"*/5 * * * *", 5, true},
		{"*/30 * * * *", 30, true},
		{"0 * * * *", 0, false},
		{"bad", 0, false},
	}
	for _, c := range cases {
		m, e := cronEveryNMinutes(c.expr)
		if m != c.mins || e != c.every {
			t.Errorf("%q: got (%d,%v) want (%d,%v)", c.expr, m, e, c.mins, c.every)
		}
	}
}

func TestFindingsSortedAndSeverityString(t *testing.T) {
	if !strings.Contains(Warn.String(), "warn") || !strings.Contains(Info.String(), "info") {
		t.Fatal("severity strings wrong")
	}
}

func TestD013DoubleTrigger(t *testing.T) {
	jobs := `
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	cases := []struct {
		name string
		on   string
		want int
	}{
		{"unscoped push mapping", "on:\n  push:\n  pull_request:", 1},
		{"sequence form", "on: [push, pull_request]", 1},
		{"wildcard branches", "on:\n  push: {branches: ['**']}\n  pull_request:", 1},
		{"scoped to main", "on:\n  push: {branches: [main]}\n  pull_request:", 0},
		{"glob but specific", "on:\n  push: {branches: ['release/*']}\n  pull_request:", 0},
		{"tags only", "on:\n  push: {tags: ['v*']}\n  pull_request:", 0},
		{"branches-ignore", "on:\n  push: {branches-ignore: [gh-pages]}\n  pull_request:", 0},
		{"push only", "on:\n  push:", 0},
		{"pull_request only", "on:\n  pull_request:", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rules(lintYAML(t, c.on+jobs))["D013"]
			if got != c.want {
				t.Fatalf("%s: want %d D013, got %d", c.name, c.want, got)
			}
		})
	}
}

func TestD014TopOfHourCron(t *testing.T) {
	y := `
on:
  schedule:
    - cron: "0 6 * * 1"
    - cron: "23 4 * * *"
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	fs := lintYAML(t, y)
	if rules(fs)["D014"] != 1 {
		t.Fatalf("expected 1 D014, got %v", fs)
	}
}

func TestD005EveryMinuteCron(t *testing.T) {
	y := `
on:
  schedule:
    - cron: "* * * * *"
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	fs := lintYAML(t, y)
	if rules(fs)["D005"] != 1 {
		t.Fatalf("expected 1 D005 for bare-* minute, got %v", fs)
	}
	for _, f := range fs {
		if f.Rule == "D005" && !strings.Contains(f.Message, "every minute") {
			t.Fatalf("want 'every minute' phrasing, got %q", f.Message)
		}
	}
}

func TestD015RetiredAction(t *testing.T) {
	cases := []struct {
		name string
		uses string
		want int
	}{
		{"upload v3", "actions/upload-artifact@v3", 1},
		{"upload v3 exact", "actions/upload-artifact@v3.1.2", 1},
		{"download v2", "actions/download-artifact@v2", 1},
		{"upload v4 ok", "actions/upload-artifact@v4", 0},
		{"cache v1", "actions/cache@v1", 1},
		{"cache v2", "actions/cache@v2", 1},
		{"cache restore v2", "actions/cache/restore@v2", 1},
		{"cache v3 ok (floating v3 points at 3.4+)", "actions/cache@v3", 0},
		{"cache v4 ok", "actions/cache@v4", 0},
		{"sha pin skipped", "actions/upload-artifact@6f51ac03b9356f520e9adb1b1b7802705f340c2b", 0},
		{"branch ref skipped", "actions/cache@main", 0},
		{"bare digit major", "actions/cache@2", 1},
		{"unrelated action", "docker/build-push-action@v2", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			y := `
on: {pull_request: null}
concurrency: {group: g, cancel-in-progress: true}
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: ` + c.uses + `
`
			got := rules(lintYAML(t, y))["D015"]
			if got != c.want {
				t.Fatalf("%s: want %d D015, got %d", c.name, c.want, got)
			}
		})
	}
}

func TestD020DeprecatingRunner(t *testing.T) {
	cases := []struct {
		name string
		job  string
		want int
	}{
		{"scalar ubuntu-22.04", "runs-on: ubuntu-22.04", 1},
		{"scalar macos-14", "runs-on: macos-14", 1},
		{"scalar current", "runs-on: ubuntu-24.04", 0},
		{"retired is D016 not D020", "runs-on: ubuntu-20.04", 0},
		{"case-insensitive", "runs-on: Ubuntu-22.04", 1},
		{"label list", "runs-on: [macos-14-large]", 1},
		{"matrix axis", "runs-on: ${{ matrix.os }}\n    strategy:\n      matrix:\n        os: [ubuntu-22.04, ubuntu-24.04, macos-14]", 2},
		{"matrix include", "runs-on: ${{ matrix.os }}\n    strategy:\n      matrix:\n        os: [ubuntu-24.04]\n        include:\n          - os: ubuntu-22.04", 1},
		{"matrix expression not resolved", "runs-on: ${{ matrix.os || 'ubuntu-latest' }}\n    strategy:\n      matrix:\n        os: [ubuntu-22.04]", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			y := `
on: {pull_request: null}
concurrency: {group: g, cancel-in-progress: true}
jobs:
  a:
    ` + c.job + `
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
			got := rules(lintYAML(t, y))["D020"]
			if got != c.want {
				t.Fatalf("%s: want %d D020, got %d", c.name, c.want, got)
			}
		})
	}
}

func TestD016RetiredRunner(t *testing.T) {
	cases := []struct {
		name string
		job  string
		want int
	}{
		{"scalar retired", "runs-on: ubuntu-20.04", 1},
		{"scalar current", "runs-on: ubuntu-24.04", 0},
		{"windows-2019", "runs-on: windows-2019", 1},
		{"macos-13", "runs-on: macos-13", 1},
		{"case-insensitive", "runs-on: Ubuntu-20.04", 1},
		{"label list", "runs-on: [ubuntu-20.04]", 1},
		{"self-hosted list", "runs-on: [self-hosted, linux]", 0},
		{"matrix axis", "runs-on: ${{ matrix.os }}\n    strategy:\n      matrix:\n        os: [ubuntu-20.04, ubuntu-24.04, macos-12]", 2},
		{"matrix include", "runs-on: ${{ matrix.os }}\n    strategy:\n      matrix:\n        os: [ubuntu-24.04]\n        include:\n          - os: windows-2019", 1},
		{"matrix clean", "runs-on: ${{ matrix.os }}\n    strategy:\n      matrix:\n        os: [ubuntu-22.04, macos-14]", 0},
		{"matrix expression not resolved", "runs-on: ${{ matrix.os || 'ubuntu-latest' }}\n    strategy:\n      matrix:\n        os: [ubuntu-20.04]", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			y := `
on: {pull_request: null}
concurrency: {group: g, cancel-in-progress: true}
jobs:
  a:
    ` + c.job + `
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
			got := rules(lintYAML(t, y))["D016"]
			if got != c.want {
				t.Fatalf("%s: want %d D016, got %d", c.name, c.want, got)
			}
		})
	}
}
