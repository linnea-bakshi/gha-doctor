package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	cases := map[string][2]string{
		"git@github.com:owner/repo.git":     {"owner", "repo"},
		"ssh://git@github.com/owner/repo":   {"owner", "repo"},
		"https://github.com/owner/repo.git": {"owner", "repo"},
		"https://github.com/owner/repo":     {"owner", "repo"},
	}
	for url, want := range cases {
		matched := false
		for _, re := range []interface{ FindStringSubmatch(string) []string }{sshRe, httpsRe} {
			if m := re.FindStringSubmatch(url); m != nil {
				matched = true
				if m[1] != want[0] || m[2] != want[1] {
					t.Errorf("%s -> %q/%q, want %q/%q", url, m[1], m[2], want[0], want[1])
				}
			}
		}
		if !matched {
			t.Errorf("no pattern matched %s", url)
		}
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

	cmd := exec.Command(bin, "--lint-only", "--json", "--dir", dir)
	out, err := cmd.Output()
	// Fixtures contain warnings, so the CI-gating exit code must be 2.
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("want exit code 2, got err=%v", err)
	}
	var doc struct {
		Findings []struct {
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
	rules := map[string]bool{}
	for _, f := range doc.Findings {
		rules[f.Rule] = true
	}
	if !rules["D001"] {
		t.Errorf("expected D001 (missing concurrency) in %v", rules)
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
