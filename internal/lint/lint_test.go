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
