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

func TestFixNpmCiAdjacentSteps(t *testing.T) {
	// Regression for the style-blind span bug (same class as D018's
	// TestFixDeprecatedCommandsAdjacentSteps): a block-scalar step directly
	// followed by a plain-scalar step, both running `npm install`. The
	// first step's line scan must stop at its own content — overshooting
	// reaches the second step's line and emits a duplicate edit for it.
	wf := `name: CI
on:
  push:
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: |
          npm install
      - run: npm install
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
	if len(results) != 1 || len(results[0].Applied) != 2 {
		t.Fatalf("expected exactly two applied D012 fixes, got %+v", results)
	}
	if len(results[0].Skipped) != 0 || len(results[0].Failed) != 0 {
		t.Fatalf("no skips/failures expected, got %+v", results[0])
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

func TestFixBytesInMemory(t *testing.T) {
	// The playground (WASM) calls FixBytes directly: fixes must come back as
	// content, nothing on disk, and clean input must return nil.
	in := []byte(`name: ci
on:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)
	out, res, err := FixBytes("ci.yml", in, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("FixBytes: %v", err)
	}
	if out == nil {
		t.Fatal("expected fixed content, got nil")
	}
	if len(res.Applied) == 0 {
		t.Fatalf("expected applied fixes, got %+v", res)
	}
	fixed := string(out)
	if !strings.Contains(fixed, "concurrency:") || !strings.Contains(fixed, "timeout-minutes:") {
		t.Fatalf("fixed output missing expected edits:\n%s", fixed)
	}

	// Already-clean input: no change, no notes.
	out2, res2, err := FixBytes("ci.yml", out, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("FixBytes second pass: %v", err)
	}
	if out2 != nil || len(res2.Applied) != 0 {
		t.Fatalf("expected idempotent no-op, got out=%v res=%+v", out2 != nil, res2)
	}
}

func TestFixExplicitKeyJobSkipped(t *testing.T) {
	// `? job name` explicit-key syntax puts key and value on separate lines;
	// inserting timeout-minutes between them would break the YAML. The fix
	// must skip with a note, not attempt-and-refuse via the safety valve.
	in := []byte(`on: push
jobs:
  ? complex key
  : runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)
	out, res, err := FixBytes("weird.yml", in, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("FixBytes should skip cleanly, got error: %v", err)
	}
	if out != nil {
		t.Fatalf("nothing should be written, got:\n%s", out)
	}
	found := false
	for _, s := range res.Skipped {
		if strings.Contains(s, "explicit-key") && strings.Contains(s, "complex key") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected explicit-key skip note, got %+v", res)
	}
}

func TestFixPreservesCRLF(t *testing.T) {
	in := []byte("on: push\r\njobs:\r\n  x:\r\n    runs-on: ubuntu-latest\r\n    steps:\r\n      - run: npm install\r\n")
	out, res, err := FixBytes("crlf.yml", in, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("FixBytes: %v", err)
	}
	if out == nil || len(res.Applied) == 0 {
		t.Fatalf("expected fixes, got out=%v res=%+v", out != nil, res)
	}
	s := string(out)
	if n := strings.Count(s, "\n"); strings.Count(s, "\r\n") != n {
		t.Fatalf("expected fully-CRLF output, got %d LF vs %d CRLF:\n%q",
			n, strings.Count(s, "\r\n"), s)
	}
	if !strings.Contains(s, "timeout-minutes: 30\r\n") || !strings.Contains(s, "npm ci\r\n") {
		t.Fatalf("expected CRLF on inserted and replaced lines:\n%q", s)
	}
	// Mixed-EOL input is left as-is: LF stays LF even next to a CRLF line.
	mixed := []byte("on: push\r\njobs:\n  x:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm install\n")
	out2, _, err := FixBytes("mixed.yml", mixed, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("FixBytes mixed: %v", err)
	}
	if out2 == nil {
		t.Fatal("expected fixes on mixed-EOL file")
	}
	if !strings.Contains(string(out2), "on: push\r\n") {
		t.Fatalf("mixed-EOL file's existing CRLF line should survive:\n%q", out2)
	}
	if strings.Contains(string(out2), "timeout-minutes: 30\r\n") {
		t.Fatalf("mixed-EOL file should get LF inserts (left as found):\n%q", out2)
	}
}

func TestFixDirContinuesPastFailingFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("unreadable-file test is meaningless as root")
	}
	root := writeRepo(t, map[string]string{
		"aaa-broken.yml": "on: push\njobs:\n  x:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n",
		"zzz-good.yml":   "on: push\njobs:\n  x:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm install\n",
	})
	wf := filepath.Join(root, ".github", "workflows")
	if err := os.Chmod(filepath.Join(wf, "aaa-broken.yml"), 0o000); err != nil {
		t.Fatal(err)
	}
	results, err := FixDir(wf, root, nil)
	if err != nil {
		t.Fatalf("FixDir must not abort on a per-file failure: %v", err)
	}
	var sawFail, sawFix bool
	for _, r := range results {
		if strings.HasSuffix(r.Path, "aaa-broken.yml") && r.Failed != "" {
			sawFail = true
		}
		if strings.HasSuffix(r.Path, "zzz-good.yml") && len(r.Applied) > 0 {
			sawFix = true
		}
	}
	if !sawFail || !sawFix {
		t.Fatalf("expected recorded failure AND later file fixed, got %+v", results)
	}
	if got := readWF(t, root, "zzz-good.yml"); !strings.Contains(got, "npm ci") {
		t.Fatalf("later file should have been written:\n%s", got)
	}
}

func TestFixRetiredCache(t *testing.T) {
	y := `on: {push: {branches: [main]}}
jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/cache@v2   # keep my comment
        with:
          path: ~/.npm
          key: npm-${{ hashFiles('package-lock.json') }}
          restore-keys: npm-
      - uses: actions/upload-artifact@v3
        with: {name: dist, path: dist/}
`
	out, res, err := FixBytes("wf.yml", []byte(y), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected a fix to apply")
	}
	s := string(out)
	if !strings.Contains(s, "actions/cache@v4   # keep my comment") {
		t.Fatalf("cache not bumped (or comment lost):\n%s", s)
	}
	if !strings.Contains(s, "actions/upload-artifact@v3") {
		t.Fatalf("artifact action must NOT be auto-bumped:\n%s", s)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "upload-artifact@v3") {
		t.Fatalf("expected a hand-fix skip note for the artifact action, got %v", res.Skipped)
	}
	// Idempotent: a second pass changes nothing.
	out2, _, err := FixBytes("wf.yml", out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out2 != nil {
		t.Fatalf("second pass should be a no-op, got:\n%s", string(out2))
	}
}

func TestFixRetiredCacheExactPin(t *testing.T) {
	y := `on: {push: {branches: [main]}}
jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/cache/restore@v1.2.0
        with: {path: ~/.npm, key: k}
`
	out, _, err := FixBytes("wf.yml", []byte(y), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !strings.Contains(string(out), "actions/cache/restore@v4") {
		t.Fatalf("exact pin not bumped: %s", string(out))
	}
}

// Fuzzer catch (v0.26.1 → v0.27): the fix used `m <= 2` while the rule uses
// the retired-majors set {1, 2}, so `actions/cache@0` (major 0 — never a
// retired major; likely a typo'd pin) produced an edit with no matching
// finding and tripped the safety valve. Fix and rule now share
// retiredActionFor; refs the rule doesn't flag must be left alone entirely.
func TestFixRetiredCacheOnlyRetiredMajors(t *testing.T) {
	for _, ref := range []string{"actions/cache@0", "actions/cache@v0.9.1", "ACtions/CAChe@0", "actions/upload-artifact@0"} {
		y := "on: {push: {branches: [main]}}\njobs:\n  build:\n    runs-on: ubuntu-latest\n    timeout-minutes: 5\n    steps:\n      - uses: " + ref + "\n        with: {path: ~/.npm, key: k}\n"
		out, res, err := FixBytes("wf.yml", []byte(y), nil, nil)
		if err != nil {
			t.Fatalf("%s: safety valve fired: %v", ref, err)
		}
		if out != nil {
			t.Fatalf("%s: major 0 is not a retired major, must not be edited:\n%s", ref, string(out))
		}
		for _, sk := range res.Skipped {
			if strings.Contains(sk, "D015") {
				t.Fatalf("%s: no D015 finding exists, so no D015 skip note should either: %v", ref, sk)
			}
		}
	}
	// Case-insensitive matching still fixes genuinely retired majors.
	y := "on: {push: {branches: [main]}}\njobs:\n  build:\n    runs-on: ubuntu-latest\n    timeout-minutes: 5\n    steps:\n      - uses: ACtions/CAChe@2\n        with: {path: ~/.npm, key: k}\n"
	out, _, err := FixBytes("wf.yml", []byte(y), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !strings.Contains(string(out), "ACtions/CAChe@v4") {
		t.Fatalf("mixed-case retired pin not bumped: %s", string(out))
	}
}

func TestDropDriftedEdits(t *testing.T) {
	// A fixer whose trigger condition drifts from its rule plans edits the
	// rule never flagged. Those must degrade to a loud skip, never an edit.
	findings := []Finding{
		{Rule: "D002", Line: 7},
		{Rule: "D014", Line: 3},
	}
	edits := []edit{
		{line: 8, rule: "D002", note: "ok", findingLine: 7},      // matches
		{line: 4, rule: "D014", note: "ok", findingLine: 3},      // matches
		{line: 12, rule: "D002", note: "drift", findingLine: 11}, // wrong line
		{line: 4, rule: "D015", note: "drift", findingLine: 3},   // wrong rule
	}
	kept, skips := dropDriftedEdits(edits, findings)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept edits, got %d: %+v", len(kept), kept)
	}
	for _, e := range kept {
		if e.note != "ok" {
			t.Fatalf("drifted edit survived the guard: %+v", e)
		}
	}
	if len(skips) != 2 {
		t.Fatalf("expected 2 skip notes, got %d: %v", len(skips), skips)
	}
	for _, s := range skips {
		if !strings.Contains(s, "fixer/rule drift") {
			t.Fatalf("skip note should name the drift class: %q", s)
		}
	}
}

func TestFixCorpusNeverTripsDriftGuard(t *testing.T) {
	// Every fixture in the odd-YAML corpus must fix without the drift guard
	// firing: fixers and rules agree on exactly which lines are findings.
	// If this fails, a fixer's trigger condition (or its findingLine, which
	// also drives inline-ignore suppression) has drifted from its rule.
	dir := filepath.Join("testdata", "oddyaml")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	pm := map[string]string{"node": "package-lock.json", "go": "go.sum"}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		_, res, err := FixBytes(name, data, pm, map[string]bool{})
		if err != nil {
			t.Fatalf("%s: safety valve fired: %v", name, err)
		}
		for _, s := range res.Skipped {
			if strings.Contains(s, "drift") {
				t.Errorf("%s: drift guard fired: %s", name, s)
			}
		}
		n++
	}
	if n == 0 {
		t.Fatal("corpus is empty — wrong path?")
	}
}

func TestFixRefusesLoneCarriageReturns(t *testing.T) {
	// A bare \r is a line break to the YAML parser but not to a \n-split
	// line array, so node positions past it point at the wrong text line.
	// Fixing must refuse loudly instead of editing a guessed line (fuzz
	// caught a D002 insert landing on the wrong line of such a file).
	in := []byte("jobs:\n 0: \r  {{}}")
	out, res, err := FixBytes("w.yml", in, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("safety valve fired: %v", err)
	}
	if out != nil {
		t.Fatalf("expected no edit, got:\n%s", out)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "carriage return") {
		t.Fatalf("expected a lone-CR skip note, got %+v", res)
	}

	// Mixed CRLF/LF (every \r pairs with \n) must still be fixable.
	mixed := []byte("name: ci\r\non: push\njobs:\r\n  a:\r\n    runs-on: ubuntu-latest\r\n    steps:\n      - run: echo hi\r\n")
	out2, res2, err := FixBytes("m.yml", mixed, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("mixed-EOL fix: %v", err)
	}
	if out2 == nil || len(res2.Applied) == 0 {
		t.Fatalf("mixed CRLF/LF file should still get fixes, got %+v", res2)
	}
}

func TestFixSkipsExplicitKeyJobBody(t *testing.T) {
	// A job whose body begins with an explicit-key (`?`) entry: the yaml
	// node's column points past the `?`, so a column-derived indent would
	// produce invalid YAML (fuzz caught this). Must skip with a note.
	in := []byte("jobs:\n 0:\n  ?")
	out, res, err := FixBytes("w.yml", in, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("safety valve fired: %v", err)
	}
	if out != nil {
		t.Fatalf("expected no edit, got:\n%s", out)
	}
	found := false
	for _, s := range res.Skipped {
		if strings.Contains(s, "D002") && strings.Contains(s, "explicit-key") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected D002 explicit-key skip note, got %+v", res)
	}
}

func TestFixSkipsExplicitKeyMarkerOnOwnLine(t *testing.T) {
	// The `?` marker alone on a line, value below it: yaml.v3 reports the
	// key node at the VALUE's position, so the same-line prefix check
	// can't see the marker and the insert used to land inside the
	// explicit key (fuzz crasher fda14b8fc0a4aace). Must skip loudly.
	in := []byte("jobs:\n 0:\n  ?\n   0")
	out, res, err := FixBytes("w.yml", in, map[string]string{}, map[string]bool{})
	if err != nil {
		t.Fatalf("safety valve fired: %v", err)
	}
	if out != nil {
		t.Fatalf("expected no edit, got:\n%s", out)
	}
	found := false
	for _, s := range res.Skipped {
		if strings.Contains(s, "D002") && strings.Contains(s, "explicit-key") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected D002 explicit-key skip note, got %+v", res)
	}
}

func TestFixRunnerLabels(t *testing.T) {
	y := `on: {push: {branches: [main]}}
jobs:
  a:
    runs-on: ubuntu-20.04   # keep my comment
    timeout-minutes: 5
    steps: [{run: echo hi}]
  b:
    runs-on: ubuntu-22.04
    timeout-minutes: 5
    steps: [{run: echo hi}]
  c:
    runs-on: windows-2019
    timeout-minutes: 5
    steps: [{run: echo hi}]
  d:
    runs-on: macos-14
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	out, res, err := FixBytes("wf.yml", []byte(y), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected fixes to apply")
	}
	s := string(out)
	if !strings.Contains(s, "runs-on: ubuntu-24.04   # keep my comment") {
		t.Fatalf("retired ubuntu not bumped (or comment lost):\n%s", s)
	}
	if strings.Contains(s, "ubuntu-22.04") {
		t.Fatalf("deprecating ubuntu-22.04 should be bumped:\n%s", s)
	}
	if !strings.Contains(s, "windows-2019") || !strings.Contains(s, "macos-14") {
		t.Fatalf("windows/macos labels must NOT be auto-bumped:\n%s", s)
	}
	var win, mac bool
	for _, sk := range res.Skipped {
		if strings.Contains(sk, "windows-2019") && strings.Contains(sk, "your call") {
			win = true
		}
		if strings.Contains(sk, "macos-14") && strings.Contains(sk, "Xcode") {
			mac = true
		}
	}
	if !win || !mac {
		t.Fatalf("expected hand-fix skip notes for windows and macos, got %v", res.Skipped)
	}
	// Idempotent: a second pass changes nothing.
	out2, _, err := FixBytes("wf.yml", out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out2 != nil {
		t.Fatalf("second pass should be a no-op, got:\n%s", string(out2))
	}
}

// Two fixable labels in one flow-style list share a physical line;
// applyEdits replaces whole lines, so they must merge into one edit or
// the second replacement clobbers the first.
func TestFixRunnerLabelsSameLine(t *testing.T) {
	y := `on: {push: {branches: [main]}}
jobs:
  a:
    runs-on: [self-hosted, ubuntu-20.04, ubuntu-22.04]
    timeout-minutes: 5
    steps: [{run: echo hi}]
`
	out, res, err := FixBytes("wf.yml", []byte(y), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected fixes to apply")
	}
	if !strings.Contains(string(out), "runs-on: [self-hosted, ubuntu-24.04, ubuntu-24.04]") {
		t.Fatalf("both labels on the line should be bumped:\n%s", string(out))
	}
	if len(res.Applied) == 0 {
		t.Fatal("expected an applied note")
	}
}

// Matrix values may be referenced in if:/include:/exclude: expressions
// the linter can't see — never rewrite them, even when the target is
// mechanical. Skip note instead.
func TestFixRunnerLabelsMatrixSkipped(t *testing.T) {
	y := `on: {push: {branches: [main]}}
jobs:
  a:
    runs-on: ${{ matrix.os }}
    timeout-minutes: 5
    strategy:
      matrix:
        os: [ubuntu-20.04, ubuntu-24.04]
    steps:
      - run: echo hi
        if: matrix.os == 'ubuntu-20.04'
`
	out, res, err := FixBytes("wf.yml", []byte(y), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && strings.Contains(string(out), "os: [ubuntu-24.04, ubuntu-24.04]") {
		t.Fatalf("matrix value must not be rewritten:\n%s", string(out))
	}
	found := false
	for _, sk := range res.Skipped {
		if strings.Contains(sk, "matrix value") && strings.Contains(sk, "ubuntu-20.04") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a matrix skip note, got %v", res.Skipped)
	}
}
