package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func TestResolveRepoFlag(t *testing.T) {
	owner, name, err := resolveRepo("cli/cli", ".")
	if err != nil || owner != "cli" || name != "cli" {
		t.Fatalf("got %q/%q, %v", owner, name, err)
	}
	if _, _, err := resolveRepo("not-a-repo", "."); err == nil {
		t.Fatal("want error for repo without slash")
	}
}

func TestRemoteURLPatterns(t *testing.T) {
	cases := map[string][3]string{
		"git@github.com:owner/repo.git":          {"github.com", "owner", "repo"},
		"ssh://git@github.com/owner/repo":        {"github.com", "owner", "repo"},
		"https://github.com/owner/repo.git":      {"github.com", "owner", "repo"},
		"https://github.com/owner/repo":          {"github.com", "owner", "repo"},
		"git@ghe.example.com:owner/repo.git":     {"ghe.example.com", "owner", "repo"},
		"https://ghe.example.com/owner/repo.git": {"ghe.example.com", "owner", "repo"},
		"https://user@ghe.example.com/owner/r":   {"ghe.example.com", "owner", "r"},
	}
	for url, want := range cases {
		matched := false
		for _, re := range []interface{ FindStringSubmatch(string) []string }{sshRe, httpsRe} {
			if m := re.FindStringSubmatch(url); m != nil {
				matched = true
				if m[1] != want[0] || m[2] != want[1] || m[3] != want[2] {
					t.Errorf("%s -> %q %q/%q, want %q %q/%q", url, m[1], m[2], m[3], want[0], want[1], want[2])
				}
			}
		}
		if !matched {
			t.Errorf("no pattern matched %s", url)
		}
	}
}

// TestResolveRepoHostMismatch: a git remote on a different host than the one
// in effect must be rejected with a hint, not silently queried on the wrong
// API endpoint.
func TestResolveRepoHostMismatch(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://ghe.example.com/acme/widgets.git"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v (%s)", err, out)
		}
	}

	t.Setenv("GH_HOST", "")
	t.Setenv("GITHUB_API_URL", "")
	if _, _, err := resolveRepo("", dir); err == nil || !strings.Contains(err.Error(), "GH_HOST=ghe.example.com") {
		t.Fatalf("want host-mismatch error naming GH_HOST, got %v", err)
	}

	t.Setenv("GH_HOST", "ghe.example.com")
	owner, name, err := resolveRepo("", dir)
	if err != nil || owner != "acme" || name != "widgets" {
		t.Fatalf("with GH_HOST set: got %q/%q, %v", owner, name, err)
	}
}

// TestIntegrationLintJSON builds the real binary and runs it in --lint-only
// --json mode against the shared fixtures, checking output and exit code.
func TestIntegrationLintJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Fixture repo: .github/workflows -> copies of testdata/workflows.
	dir := t.TempDir()
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "..", "testdata", "workflows")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wfDir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Plus a published action manifest at the root: the second lint surface.
	act := "name: fixture\nruns:\n  using: node20\n  main: dist/index.js\n"
	if err := os.WriteFile(filepath.Join(dir, "action.yml"), []byte(act), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "--lint-only", "--json", "--dir", dir)
	out, err := cmd.Output()
	// Fixtures contain warnings, so the CI-gating exit code must be 2.
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("want exit code 2, got err=%v", err)
	}
	var doc struct {
		FilesScanned int `json:"files_scanned"`
		Findings     []struct {
			Rule     string `json:"rule"`
			File     string `json:"file"`
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(doc.Findings) == 0 {
		t.Fatal("expected findings from fixture workflows")
	}
	if doc.FilesScanned == 0 {
		t.Error("files_scanned missing from --json output")
	}
	rules := map[string]bool{}
	for _, f := range doc.Findings {
		rules[f.Rule] = true
	}
	if !rules["D001"] {
		t.Errorf("expected D001 (missing concurrency) in %v", rules)
	}
	// The fixture repo has no dependabot/renovate config, so the repo-level
	// D017 must fire — as info, pointing at the missing dependabot path.
	sawD017 := false
	for _, f := range doc.Findings {
		if f.Rule == "D017" {
			sawD017 = true
			if f.Severity != "info" || f.File != ".github/dependabot.yml" {
				t.Errorf("D017: want info at .github/dependabot.yml, got %+v", f)
			}
		}
	}
	if !sawD017 {
		t.Error("expected repo-level D017 (no update automation)")
	}
	// The root action.yml declares node20 → D019, counted in files_scanned.
	sawD019 := false
	for _, f := range doc.Findings {
		if f.Rule == "D019" {
			sawD019 = true
			if f.Severity != "warning" || f.File != "action.yml" {
				t.Errorf("D019: want warning at action.yml, got %+v", f)
			}
		}
	}
	if !sawD019 {
		t.Error("expected D019 (deprecated action runtime) from fixture action.yml")
	}

	// --annotate appends workflow commands with repo-relative paths after
	// the report; with --json it must skip (stderr note) to keep stdout pure.
	cmd = exec.Command(bin, "--lint-only", "--annotate", "--dir", dir)
	aout, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("--annotate: want exit code 2, got err=%v", err)
	}
	if !strings.Contains(string(aout), "::warning file=.github/workflows/") {
		t.Errorf("--annotate: expected a repo-relative ::warning command in output:\n%s", aout)
	}
	cmd = exec.Command(bin, "--lint-only", "--json", "--annotate", "--dir", dir)
	var jerr strings.Builder
	cmd.Stderr = &jerr
	jout, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("--annotate --json: want exit code 2, got err=%v", err)
	}
	if strings.Contains(string(jout), "::warning") {
		t.Errorf("--annotate with --json must not write commands into the JSON stream")
	}
	if !strings.Contains(jerr.String(), "--annotate skipped") {
		t.Errorf("--annotate with --json should note the skip on stderr, got: %s", jerr.String())
	}
	if err := json.Unmarshal(jout, &doc); err != nil {
		t.Errorf("--annotate --json: stdout no longer valid JSON: %v", err)
	}

	// A bad flag must exit 1, not 2: the action treats exit 2 as "findings
	// found", and a usage error must never masquerade as a clean-ish gate.
	cmd = exec.Command(bin, "--no-such-flag")
	if err := cmd.Run(); err == nil {
		t.Fatal("bad flag should fail")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("bad flag: want exit 1, got %v", err)
	}

	// --version must not run any checks.
	vout, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if string(vout) == "" {
		t.Error("--version printed nothing")
	}
}

func TestIntegrationRemoteFixRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	// A directory with local workflows but no git remote: --repo X --fix must
	// refuse rather than "fix" unrelated local files while grading repo X.
	dir := t.TempDir()
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte("on: push\njobs: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "--repo", "some/other", "--fix")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 1 {
		t.Fatalf("want exit 1, got err=%v out=%s", err, out)
	}
	if !strings.Contains(string(out), "refusing to --fix") {
		t.Errorf("expected refusal message, got: %s", out)
	}

	// With an explicit --dir the user has been unambiguous: fix locally.
	cmd = exec.Command(bin, "--repo", "some/other", "--fix", "--dir", dir)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--fix --dir should run locally, got err=%v out=%s", err, out)
	}
	if !strings.Contains(string(out), "fix") {
		t.Errorf("expected fix output, got: %s", out)
	}
}

// TestIntegrationBaseline builds the binary and exercises --baseline against
// a real git repo: pre-existing findings are hidden, only the finding
// introduced after the baseline commit is reported (and gates exit 2).
func TestIntegrationBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Baseline workflow: D001 (no concurrency) + D002 (no timeout) on `old`.
	base := "on: pull_request\njobs:\n  old:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
	wf := filepath.Join(wfDir, "ci.yml")
	if err := os.WriteFile(wf, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "base")

	// New commit adds a second job without timeout: one NEW D002; the shifted
	// baseline findings must stay hidden despite different line numbers.
	cur := "on: pull_request\n\njobs:\n  old:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n  fresh:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo new\n"
	if err := os.WriteFile(wf, []byte(cur), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "--lint-only", "--json", "--baseline", "main")
	cmd.Dir = dir
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("want exit 2 (new warning), got err=%v\n%s", err, out)
	}
	var doc struct {
		Findings []struct {
			Rule    string `json:"rule"`
			Message string `json:"message"`
		} `json:"findings"`
		Baseline *struct {
			Ref    string `json:"ref"`
			Hidden int    `json:"hidden"`
			Fixed  int    `json:"fixed"`
		} `json:"baseline"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if doc.Baseline == nil || doc.Baseline.Ref != "main" {
		t.Fatalf("baseline block missing: %s", out)
	}
	if len(doc.Findings) != 1 || doc.Findings[0].Rule != "D002" || !strings.Contains(doc.Findings[0].Message, "fresh") {
		t.Fatalf("want exactly the new D002 on job `fresh`, got %s", out)
	}
	if doc.Baseline.Hidden != 2 || doc.Baseline.Fixed != 0 {
		t.Fatalf("want hidden=2 fixed=0, got %+v", doc.Baseline)
	}

	// No changes vs baseline: exit 0 and zero findings even though the file
	// itself has warnings.
	if err := os.WriteFile(wf, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(bin, "--lint-only", "--json", "--baseline", "main")
	cmd.Dir = dir
	out, err = cmd.Output()
	if err != nil {
		t.Fatalf("want exit 0 when nothing is new, got %v\n%s", err, out)
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if len(doc.Findings) != 0 || doc.Baseline.Hidden != 2 {
		t.Fatalf("want 0 new findings / 2 hidden, got %s", out)
	}

	// Unknown ref: clear error, exit 1.
	cmd = exec.Command(bin, "--lint-only", "--baseline", "no-such-ref")
	cmd.Dir = dir
	cout, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("want exit 1 for bad ref, got err=%v\n%s", err, cout)
	}
	if !strings.Contains(string(cout), "fetch the base branch") {
		t.Fatalf("error should hint at fetching the base branch:\n%s", cout)
	}
}

// A bare `gha-doctor` in a directory with no workflows and no git remote
// must not exit 0 in silence — it should say what would make it useful.
func TestIntegrationNothingToScan(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	dir := t.TempDir() // empty: no .github/workflows, no git repo
	cmd := exec.Command(bin)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("want exit 1 when there is nothing to scan, got err=%v\n%s", err, out)
	}
	for _, want := range []string{"nothing to scan", "--repo OWNER/NAME", "--help"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("message should contain %q:\n%s", want, out)
		}
	}
}

func TestIntegrationDiffPreview(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	dir := t.TempDir()
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := []byte(`name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)
	path := filepath.Join(wfDir, "ci.yml")
	if err := os.WriteFile(path, wf, 0o644); err != nil {
		t.Fatal(err)
	}

	// Terminal mode: a unified diff, nothing written, exit 0.
	out, err := exec.Command(bin, "--diff", "--dir", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("--diff should exit 0, got %v\n%s", err, out)
	}
	for _, want := range []string{"--- a/", "+++ b/", "+    timeout-minutes:", "nothing was written", "apply with --fix"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("--diff output missing %q:\n%s", want, out)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(wf) {
		t.Fatal("--diff modified the workflow file")
	}

	// JSON mode: structured preview with a diff per file.
	out, err = exec.Command(bin, "--diff", "--json", "--dir", dir).Output()
	if err != nil {
		t.Fatalf("--diff --json should exit 0, got %v", err)
	}
	var doc struct {
		FixPreview []struct {
			Path    string   `json:"path"`
			Applied []string `json:"applied"`
			Diff    string   `json:"diff"`
		} `json:"fix_preview"`
		FixesAvailable int `json:"fixes_available"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("--diff --json output invalid: %v\n%s", err, out)
	}
	if doc.FixesAvailable == 0 || len(doc.FixPreview) == 0 {
		t.Fatalf("expected fixes in --diff --json output:\n%s", out)
	}
	if !strings.Contains(doc.FixPreview[0].Diff, "@@") {
		t.Fatalf("json diff missing hunk header:\n%s", doc.FixPreview[0].Diff)
	}

	// --diff and --fix are mutually exclusive; exit 1 (not 2: that means findings).
	out, err = exec.Command(bin, "--diff", "--fix", "--dir", dir).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("--diff --fix should exit 1, got %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--diff previews") {
		t.Fatalf("conflict message missing:\n%s", out)
	}
}

// TestIntegrationConfigFile: .gha-doctor.yml disables rules repo-wide, is
// disclosed on stderr and in --json, unions with --disable, warns loudly on
// typos, and --no-config restores the unconfigured behavior.
func TestIntegrationConfigFile(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	dir := t.TempDir()
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One job, no timeout (D002 warn), npm install (D012 info),
	// no concurrency w/ pull_request (D001 warn).
	wf := `on: [push, pull_request]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: npm install
`
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gha-doctor.yml"),
		[]byte("disable: [D002, D017]\nbogus-key: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (rules map[string]bool, cfgFile string, stderr string, exitCode int) {
		cmd := exec.Command(bin, append([]string{"--lint-only", "--json", "--dir", dir}, args...)...)
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		out, err := cmd.Output()
		exitCode = 0
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run: %v", err)
		}
		var doc struct {
			Findings []struct {
				Rule string `json:"rule"`
			} `json:"findings"`
			Config *struct {
				File    string   `json:"file"`
				Disable []string `json:"disable"`
			} `json:"config"`
		}
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("bad JSON: %v\n%s", err, out)
		}
		rules = map[string]bool{}
		for _, f := range doc.Findings {
			rules[f.Rule] = true
		}
		if doc.Config != nil {
			cfgFile = doc.Config.File
		}
		return rules, cfgFile, errBuf.String(), exitCode
	}

	// Config applies: D002/D017 gone, D001/D012 remain, exit still 2.
	rules, cfgFile, stderr, code := run()
	if rules["D002"] || rules["D017"] {
		t.Errorf("config-disabled rules still present: %v", rules)
	}
	if !rules["D001"] || !rules["D012"] {
		t.Errorf("expected D001+D012 to survive, got %v", rules)
	}
	if code != 2 {
		t.Errorf("want exit 2 (D001 warn remains), got %d", code)
	}
	if cfgFile != ".gha-doctor.yml" {
		t.Errorf("json config.file = %q", cfgFile)
	}
	if !strings.Contains(stderr, "config: .gha-doctor.yml — disable D002, D017") {
		t.Errorf("stderr missing config note:\n%s", stderr)
	}
	if !strings.Contains(stderr, `unknown key "bogus-key"`) {
		t.Errorf("stderr missing unknown-key warning:\n%s", stderr)
	}

	// --disable unions with the config.
	rules, _, _, _ = run("--disable", "D012")
	if rules["D002"] || rules["D012"] {
		t.Errorf("union of config+CLI disable failed: %v", rules)
	}

	// --no-config restores everything.
	rules, cfgFile, stderr, _ = run("--no-config")
	if !rules["D002"] || !rules["D017"] {
		t.Errorf("--no-config should restore D002+D017, got %v", rules)
	}
	if cfgFile != "" || strings.Contains(stderr, "config: .gha-doctor.yml —") {
		t.Errorf("--no-config must not report a config (file=%q)\n%s", cfgFile, stderr)
	}

	// A broken config is loud but not fatal.
	if err := os.WriteFile(filepath.Join(dir, ".gha-doctor.yml"), []byte("disable: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, _, stderr, code = run()
	if !rules["D002"] {
		t.Errorf("broken config should leave rules enabled, got %v", rules)
	}
	if code != 2 {
		t.Errorf("broken config: want exit 2 from findings, got %d", code)
	}
	if !strings.Contains(stderr, "running unconfigured") {
		t.Errorf("stderr missing broken-config note:\n%s", stderr)
	}
}

// The scaffold --init writes must lint clean under our own rules: a new
// rule that flags it should fail this test, not embarrass a new user.
func TestInitWorkflowLintsClean(t *testing.T) {
	findings, err := lint.LintBytes(initRelPath, []byte(initWorkflow))
	if err != nil {
		t.Fatalf("scaffold does not parse: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("scaffold must lint clean, got %d findings: %+v", len(findings), findings)
	}
}

func TestIntegrationInit(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	dir := t.TempDir()
	out, err := exec.Command(bin, "--init", "--dir", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("--init failed: %v\n%s", err, out)
	}
	path := filepath.Join(dir, ".github", "workflows", "gha-doctor.yml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scaffold not written: %v", err)
	}
	if string(got) != initWorkflow {
		t.Fatal("written file differs from template")
	}
	if !strings.Contains(string(out), "git add") {
		t.Fatalf("output should include next steps:\n%s", out)
	}

	// Second run must refuse to overwrite, exit 1.
	out, err = exec.Command(bin, "--init", "--dir", dir).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("re-init should exit 1, got err=%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "already exists") {
		t.Fatalf("re-init message:\n%s", out)
	}

	// Combining with another mode flag is a usage error (exit 1, not 2).
	out, err = exec.Command(bin, "--init", "--json", "--dir", dir).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("--init --json should exit 1, got err=%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "cannot be combined") {
		t.Fatalf("conflict message:\n%s", out)
	}
}

// --workflow scopes the history sample; whole-repo and no-history modes
// must refuse it (exit 1 — never 2, which means "findings found").
func TestIntegrationWorkflowFlagConflicts(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--workflow", "ci.yml", "--lint-only"}, "--lint-only"},
		{[]string{"--workflow", "ci.yml", "--org", "x"}, "--org"},
		{[]string{"--workflow", "ci.yml", "--run", "latest"}, "--run"},
		{[]string{"--workflow", "ci.yml", "--sarif"}, "--sarif"},
		{[]string{"--workflow", "ci.yml", "--fix"}, "--fix"},
		{[]string{"--workflow", "ci.yml", "--diff"}, "--diff"},
		{[]string{"--workflow", "ci.yml", "--baseline", "main"}, "--baseline"},
		{[]string{"--workflow", "ci.yml", "--badge", "b.svg"}, "whole-repo"},
		{[]string{"--workflow", "ci.yml", "--score-history", "s.jsonl"}, "whole-repo"},
	} {
		cmd := exec.Command(bin, tc.args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 1 {
			t.Errorf("%v: want exit 1, got %v", tc.args, err)
			continue
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("%v: stderr %q missing %q", tc.args, stderr.String(), tc.want)
		}
	}
}

// TestIntegrationMCP drives the real binary as an MCP stdio server:
// handshake, tools/list, an offline lint_repo call against a fixture
// directory, and explain_rule. Everything here is offline.
func TestIntegrationMCP(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	// --mcp refuses company.
	guard := exec.Command(bin, "--mcp", "--repo", "some/repo")
	var guardErr bytes.Buffer
	guard.Stderr = &guardErr
	if err := guard.Run(); err == nil {
		t.Fatal("--mcp --repo must exit nonzero")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("--mcp --repo must exit 1, got %v", err)
	}
	if !strings.Contains(guardErr.String(), "cannot be combined") {
		t.Fatalf("guard message missing: %q", guardErr.String())
	}

	// Fixture dir with one workflow that has known findings.
	dir := t.TempDir()
	wfDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "on: pull_request\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n"
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := exec.Command(bin, "--mcp")
	stdin, err := srv.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := srv.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Process.Kill()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<21)

	send := func(s string) {
		t.Helper()
		if _, err := stdin.Write([]byte(s + "\n")); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	recv := func() map[string]any {
		t.Helper()
		if !sc.Scan() {
			t.Fatalf("no response: %v", sc.Err())
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad JSON %q: %v", sc.Text(), err)
		}
		return m
	}
	mustResult := func(m map[string]any) map[string]any {
		t.Helper()
		r, ok := m["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected result, got %v", m)
		}
		return r
	}
	callText := func(r map[string]any) (string, bool) {
		t.Helper()
		content, ok := r["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("no content in %v", r)
		}
		c := content[0].(map[string]any)
		if c["type"] != "text" {
			t.Fatalf("want text content, got %v", c)
		}
		return c["text"].(string), r["isError"] == true
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	init := mustResult(recv())
	if init["protocolVersion"] != "2025-06-18" {
		t.Fatalf("bad negotiated version: %v", init["protocolVersion"])
	}
	if init["serverInfo"].(map[string]any)["name"] != "gha-doctor" {
		t.Fatalf("bad serverInfo: %v", init["serverInfo"])
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := mustResult(recv())["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		td := tl.(map[string]any)
		names[td["name"].(string)] = true
		if td["description"] == "" || td["inputSchema"] == nil {
			t.Fatalf("tool %v missing description or schema", td["name"])
		}
	}
	for _, want := range []string{"analyze_repo", "lint_repo", "preview_fixes", "run_deep_dive", "org_overview", "explain_rule"} {
		if !names[want] {
			t.Fatalf("tool %s missing from tools/list (got %v)", want, names)
		}
	}

	// Offline lint of the fixture: the workflow above has no timeout-minutes
	// (D002) and no concurrency cancellation (D001).
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "lint_repo", "arguments": map[string]any{"dir": dir}},
	})
	send(string(req))
	text, isErr := callText(mustResult(recv()))
	if isErr {
		t.Fatalf("lint_repo errored: %s", text)
	}
	if !strings.Contains(text, "D001") || !strings.Contains(text, "D002") {
		t.Fatalf("lint_repo must report D001+D002 findings, got: %s", text)
	}

	send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"explain_rule","arguments":{"rule":"d002"}}}`)
	text, isErr = callText(mustResult(recv()))
	if isErr || !strings.Contains(text, "NoJobTimeout") {
		t.Fatalf("explain_rule d002 must print the D002 doc, got isErr=%v: %.200s", isErr, text)
	}

	// Bad arguments surface as tool errors the model can correct, not crashes.
	send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"analyze_repo","arguments":{"repo":"not-a-repo"}}}`)
	text, isErr = callText(mustResult(recv()))
	if !isErr || !strings.Contains(text, "owner/name") {
		t.Fatalf("bad repo arg must be a tool error mentioning owner/name, got isErr=%v %q", isErr, text)
	}

	// Clean shutdown on stdin EOF.
	stdin.Close()
	if err := srv.Wait(); err != nil {
		t.Fatalf("server exit: %v", err)
	}
}

// TestIntegrationFailOn: --fail-on picks the severity that trips exit 2.
// Default gates on warnings only (info findings never fail a build unless
// asked), "any" gates on everything, "never" is report-only, and the config
// file's fail-on loses to an explicit flag.
func TestIntegrationFailOn(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	bin := filepath.Join(t.TempDir(), "gha-doctor")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	mkRepo := func(t *testing.T, workflow string) string {
		dir := t.TempDir()
		wfDir := filepath.Join(dir, ".github", "workflows")
		if err := os.MkdirAll(wfDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(workflow), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	exitCode := func(t *testing.T, args ...string) (int, string) {
		t.Helper()
		var stderr bytes.Buffer
		cmd := exec.Command(bin, args...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			return 0, string(out)
		}
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %v: %v (stderr: %s)", args, err, stderr.String())
		}
		return ee.ExitCode(), string(out)
	}

	// Info-only fixture: weekly cron at minute 0 (D014) with no repository
	// guard (D021); timeout set so D002 stays quiet.
	infoRepo := mkRepo(t, `on:
  schedule:
    - cron: '0 6 * * 1'
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: echo hi
`)
	// Warning fixture: no timeout-minutes (D002, warning).
	warnRepo := mkRepo(t, `on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)

	// The info fixture really is info-only, and really has findings —
	// asserted, not assumed, so a future severity change can't hollow out
	// this test.
	rc, out := exitCode(t, "--lint-only", "--json", "--dir", infoRepo)
	var doc struct {
		Findings []struct {
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(doc.Findings) == 0 {
		t.Fatal("info fixture produced no findings; the fixture needs updating")
	}
	for _, f := range doc.Findings {
		if f.Severity != "info" {
			t.Fatalf("info fixture produced a %q finding; the fixture needs updating", f.Severity)
		}
	}
	if rc != 0 {
		t.Errorf("info-only findings, default gate: want exit 0, got %d", rc)
	}

	if rc, _ := exitCode(t, "--lint-only", "--fail-on", "any", "--dir", infoRepo); rc != 2 {
		t.Errorf("info-only findings, --fail-on any: want exit 2, got %d", rc)
	}
	if rc, _ := exitCode(t, "--lint-only", "--dir", warnRepo); rc != 2 {
		t.Errorf("warning findings, default gate: want exit 2, got %d", rc)
	}
	if rc, _ := exitCode(t, "--lint-only", "--fail-on", "never", "--dir", warnRepo); rc != 0 {
		t.Errorf("warning findings, --fail-on never: want exit 0, got %d", rc)
	}

	// A bad value must exit 1 (usage error), never 2 (findings).
	var stderr bytes.Buffer
	cmd := exec.Command(bin, "--lint-only", "--fail-on", "sometimes", "--dir", warnRepo)
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
		t.Fatalf("bad --fail-on: want exit 1, got %v", err)
	}
	if !strings.Contains(stderr.String(), "--fail-on") {
		t.Errorf("bad --fail-on stderr should name the flag: %s", stderr.String())
	}

	// Config file sets the repo policy; the explicit flag still wins.
	if err := os.WriteFile(filepath.Join(warnRepo, ".gha-doctor.yml"), []byte("fail-on: never\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc, _ := exitCode(t, "--lint-only", "--dir", warnRepo); rc != 0 {
		t.Errorf("config fail-on never: want exit 0, got %d", rc)
	}
	if rc, _ := exitCode(t, "--lint-only", "--fail-on", "warning", "--dir", warnRepo); rc != 2 {
		t.Errorf("--fail-on warning must beat config never: want exit 2, got %d", rc)
	}
}
