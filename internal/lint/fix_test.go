package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepo(t *testing.T, workflows map[string]string, rootFiles ...string) string {
	t.Helper()
	root := t.TempDir()
	wf := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range workflows {
		if err := os.WriteFile(filepath.Join(wf, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range rootFiles {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readWF(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const ciMissingAll = `name: CI
on:
  pull_request:
  push:
    branches: [main]

# build and test
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
      - run: npm ci && npm test
`

func TestFixAddsConcurrencyTimeoutAndCache(t *testing.T) {
	root := writeRepo(t, map[string]string{"ci.yml": ciMissingAll}, "package-lock.json")
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if len(results[0].Applied) != 3 {
		t.Fatalf("want 3 fixes, got %v (skipped %v)", results[0].Applied, results[0].Skipped)
	}
	out := readWF(t, root, "ci.yml")
	for _, want := range []string{
		"concurrency:",
		"cancel-in-progress: true",
		"timeout-minutes: 30",
		"cache: npm",
		"# build and test", // comment preserved
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Fixed file must lint clean for the fixable rules.
	fs, err := LintBytes("ci.yml", []byte(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Rule == "D001" || f.Rule == "D002" || f.Rule == "D003" {
			t.Errorf("finding %s survived fix: %s", f.Rule, f.Message)
		}
	}
	// Idempotent: second run applies nothing.
	results, err = FixDir(filepath.Join(root, ".github", "workflows"), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if len(r.Applied) > 0 {
			t.Errorf("second fix pass applied %v", r.Applied)
		}
	}
}

func TestFixCancelInProgress(t *testing.T) {
	missing := `on: pull_request
concurrency:
  group: ci-${{ github.ref }}
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: true
`
	falsy := strings.Replace(missing, "group: ci-${{ github.ref }}",
		"group: ci-${{ github.ref }}\n  cancel-in-progress: false", 1)

	for name, src := range map[string]string{"missing.yml": missing, "falsy.yml": falsy} {
		root := writeRepo(t, map[string]string{name: src})
		if _, err := FixDir(filepath.Join(root, ".github", "workflows"), root); err != nil {
			t.Fatal(err)
		}
		out := readWF(t, root, name)
		if !strings.Contains(out, "cancel-in-progress: true") {
			t.Errorf("%s: cancel-in-progress not fixed:\n%s", name, out)
		}
		if strings.Contains(out, "cancel-in-progress: false") {
			t.Errorf("%s: false value survived:\n%s", name, out)
		}
	}
}

func TestFixSetupCacheNoWithBlock(t *testing.T) {
	src := `on: pull_request
concurrency:
  group: g
  cancel-in-progress: true
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/setup-python@v5
      - run: pip install -r requirements.txt
`
	root := writeRepo(t, map[string]string{"py.yml": src}, "requirements.txt")
	if _, err := FixDir(filepath.Join(root, ".github", "workflows"), root); err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "py.yml")
	if !strings.Contains(out, "with:\n          cache: pip") {
		t.Errorf("with/cache block not inserted:\n%s", out)
	}
}

func TestFixSkipsAmbiguousLockfiles(t *testing.T) {
	root := writeRepo(t, map[string]string{"ci.yml": ciMissingAll}, "package-lock.json", "yarn.lock")
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if strings.Contains(out, "cache:") {
		t.Errorf("cache fix applied despite ambiguous lockfiles:\n%s", out)
	}
	found := false
	for _, r := range results {
		for _, s := range r.Skipped {
			if strings.Contains(s, "D003") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected a D003 skip note")
	}
}

func TestFixSkipsFlowStyleConcurrency(t *testing.T) {
	src := `on: pull_request
concurrency: { group: g }
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: true
`
	root := writeRepo(t, map[string]string{"flow.yml": src})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root)
	if err != nil {
		t.Fatal(err)
	}
	if out := readWF(t, root, "flow.yml"); out != src {
		t.Errorf("flow-style file was modified:\n%s", out)
	}
	if len(results) == 0 || len(results[0].Skipped) == 0 {
		t.Error("expected a skip note for flow-style concurrency")
	}
}

func TestFixReusableWorkflowJobUntouched(t *testing.T) {
	src := `on: pull_request
concurrency:
  group: g
  cancel-in-progress: true
jobs:
  call:
    uses: ./.github/workflows/other.yml
`
	root := writeRepo(t, map[string]string{"reuse.yml": src})
	if _, err := FixDir(filepath.Join(root, ".github", "workflows"), root); err != nil {
		t.Fatal(err)
	}
	if out := readWF(t, root, "reuse.yml"); strings.Contains(out, "timeout-minutes") {
		t.Errorf("reusable-workflow job got a timeout:\n%s", out)
	}
}

func TestDetectPackageManagers(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"pnpm-lock.yaml", "poetry.lock", "pom.xml"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pm := detectPackageManagers(root)
	want := map[string]string{"node": "pnpm", "python": "poetry", "java": "maven"}
	for k, v := range want {
		if pm[k] != v {
			t.Errorf("pm[%s] = %q, want %q", k, pm[k], v)
		}
	}
}
