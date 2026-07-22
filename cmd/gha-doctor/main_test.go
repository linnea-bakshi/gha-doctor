package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
