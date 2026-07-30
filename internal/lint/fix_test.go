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
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
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
	results, err = FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
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
		if _, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil); err != nil {
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
	if _, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil); err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "py.yml")
	if !strings.Contains(out, "with:\n          cache: pip") {
		t.Errorf("with/cache block not inserted:\n%s", out)
	}
}

func TestFixSkipsAmbiguousLockfiles(t *testing.T) {
	root := writeRepo(t, map[string]string{"ci.yml": ciMissingAll}, "package-lock.json", "yarn.lock")
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
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
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
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
	if _, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil); err != nil {
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

func TestFixRestoreKeys(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v4
      - uses: actions/cache@v4
        with:
          path: ~/.npm
          key: ${{ runner.os }}-npm-${{ hashFiles('**/package-lock.json') }}
      - run: npm ci
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if !strings.Contains(out, "restore-keys: |") ||
		!strings.Contains(out, "\n            ${{ runner.os }}-npm-\n") {
		t.Fatalf("restore-keys not inserted correctly:\n%s", out)
	}
	// prefix line must come right after the key line
	ki := strings.Index(out, "key: ${{")
	ri := strings.Index(out, "restore-keys:")
	if ri < ki {
		t.Fatalf("restore-keys inserted before key:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Applied) == 0 {
		t.Fatalf("expected applied fix, got %+v", results)
	}
	after, _ := LintBytes("ci.yml", []byte(out))
	if countRule(after, "D008") != 0 {
		t.Fatalf("D008 still present after fix:\n%s", out)
	}
}

func TestFixRestoreKeysSkipsNonHashKey(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/cache@v4
        with:
          path: ~/.npm
          key: static-cache-key
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if strings.Contains(out, "restore-keys") {
		t.Fatalf("should not have derived restore-keys from a static key:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Skipped) == 0 ||
		!strings.Contains(results[0].Skipped[0], "D008") {
		t.Fatalf("expected a D008 skip note, got %+v", results)
	}
}

func TestRestoreKeyPrefix(t *testing.T) {
	cases := []struct {
		key, want string
		ok        bool
	}{
		{"${{ runner.os }}-npm-${{ hashFiles('**/package-lock.json') }}", "${{ runner.os }}-npm-", true},
		{"cargo-${{ hashFiles('Cargo.lock') }}", "cargo-", true},
		{"${{ hashFiles('x') }}", "", false},  // nothing before the hash
		{"static-key", "", false},             // no expression at all
		{"${{ runner.os }}-cache", "", false}, // no hashFiles suffix
		{"pip-${{ github.sha }}", "", false},  // trailing expr isn't hashFiles
	}
	for _, c := range cases {
		got, ok := restoreKeyPrefix(c.key)
		if ok != c.ok || got != c.want {
			t.Errorf("restoreKeyPrefix(%q) = %q,%v; want %q,%v", c.key, got, ok, c.want, c.ok)
		}
	}
}

func TestFixNpmInstall(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: npm install
  b:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: |
          echo start
          npm install
          npm test
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if strings.Contains(out, "npm install") {
		t.Fatalf("npm install should be gone:\n%s", out)
	}
	if !strings.Contains(out, "- run: npm ci") || !strings.Contains(out, "\n          npm ci\n") {
		t.Fatalf("npm ci not substituted in both styles:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Applied) < 2 {
		t.Fatalf("expected two applied D012 fixes, got %+v", results)
	}
	after, _ := LintBytes("ci.yml", []byte(out))
	if countRule(after, "D012") != 0 {
		t.Fatalf("D012 still present after fix:\n%s", out)
	}
}

func TestFixNpmInstallSkipsArgs(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: npm install --legacy-peer-deps
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if !strings.Contains(out, "npm install --legacy-peer-deps") {
		t.Fatalf("npm install with args must be left alone:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Skipped) == 0 ||
		!strings.Contains(results[0].Skipped[0], "D012") {
		t.Fatalf("expected a D012 skip note, got %+v", results)
	}
}

func TestFixNpmInstallGlobalUntouched(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: npm install -g corepack
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		for _, s := range append(r.Applied, r.Skipped...) {
			if strings.Contains(s, "D012") {
				t.Fatalf("global install must not trigger D012 fix machinery: %+v", results)
			}
		}
	}
}

// Regression: an insert (D008 restore-keys) and a replace (D012 npm ci) can
// target the same original line when the npm install step directly follows
// the cache step's key. The replace must be applied before the insert shifts
// the line down, or it clobbers the inserted line.
func TestFixSameLineInsertAndReplace(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/cache@v4
        with:
          path: ~/.npm
          key: npm-${{ hashFiles('**/package-lock.json') }}
      - run: npm install
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	for _, want := range []string{"restore-keys: |", "npm-\n", "- run: npm ci"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q after same-line fix:\n%s", want, out)
		}
	}
	if strings.Contains(out, "npm install") {
		t.Fatalf("npm install survived:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Applied) < 2 {
		t.Fatalf("expected both fixes applied, got %+v", results)
	}
}

func TestFixCronMinute(t *testing.T) {
	wf := `name: nightly
on:
  schedule:
    - cron: "0 4 * * *"
    - cron: '0 12 * * 1'
    - cron: "17 6 * * 3"
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: make
`
	root := writeRepo(t, map[string]string{"nightly.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "nightly.yml")
	if strings.Contains(out, `"0 4 * * *"`) || strings.Contains(out, `'0 12 * * 1'`) {
		t.Fatalf("minute-0 crons should be rewritten:\n%s", out)
	}
	if !strings.Contains(out, `"17 6 * * 3"`) {
		t.Fatalf("non-zero-minute cron must be untouched:\n%s", out)
	}
	after, _ := LintBytes("nightly.yml", []byte(out))
	if countRule(after, "D014") != 0 {
		t.Fatalf("D014 still present after fix:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Applied) != 2 {
		t.Fatalf("expected two applied D014 fixes, got %+v", results)
	}
	// Deterministic: fixing the same input again from scratch gives the
	// same minutes; and the two crons landed on different minutes.
	root2 := writeRepo(t, map[string]string{"nightly.yml": wf})
	if _, err := FixDir(filepath.Join(root2, ".github", "workflows"), root2, nil); err != nil {
		t.Fatal(err)
	}
	if out2 := readWF(t, root2, "nightly.yml"); out2 != out {
		t.Fatalf("fix not deterministic:\n%s\nvs\n%s", out, out2)
	}
	m1 := scatterMinute("nightly.yml", "0 4 * * *", 0)
	m2 := scatterMinute("nightly.yml", "0 12 * * 1", 1)
	if m1 == m2 {
		t.Fatalf("expected scattered minutes, both got %d", m1)
	}
	if m1 < 1 || m1 > 59 || m2 < 1 || m2 > 59 {
		t.Fatalf("minutes out of range: %d %d", m1, m2)
	}
}

func TestFixCronMinuteRespectsIgnore(t *testing.T) {
	wf := `name: nightly
on:
  schedule:
    - cron: "0 4 * * *" # gha-doctor: ignore[D014]
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: make
`
	root := writeRepo(t, map[string]string{"nightly.yml": wf})
	if _, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil); err != nil {
		t.Fatal(err)
	}
	if out := readWF(t, root, "nightly.yml"); !strings.Contains(out, `"0 4 * * *"`) {
		t.Fatalf("ignored cron must not be fixed:\n%s", out)
	}
}
