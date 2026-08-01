package lint

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// fuzzSeeds loads every committed odd-YAML corpus file plus the repo's own
// workflows as seed inputs, so the fuzzer starts from realistic shapes
// (anchors, CRLF, multidoc, flow style, tabs, unicode, 1000-job files)
// instead of pure noise.
func fuzzSeeds(f *testing.F) {
	f.Helper()
	for _, dir := range []string{
		"testdata/oddyaml",
		filepath.Join("..", "..", ".github", "workflows"),
		filepath.Join("..", "..", "testdata", "workflows"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err == nil {
				f.Add(data)
			}
		}
	}
	// A few handwritten shapes the corpus doesn't cover.
	f.Add([]byte("on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"))
	f.Add([]byte("on: [push, pull_request]\njobs: {a: {runs-on: ubuntu-latest, steps: [{uses: actions/cache@v2}]}}\n"))
	f.Add([]byte("on:\n  schedule:\n    - cron: '0 * * * *'\n"))
	f.Add([]byte("jobs:\n  ? a\n  : {runs-on: ubuntu-20.04, steps: [{run: npm install}]}\n"))
}

// FuzzLintBytes asserts the linter never panics, whatever bytes it is fed.
// Workflow files come from strangers' repos over the contents API, so this
// is directly attacker-reachable surface.
func FuzzLintBytes(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = LintBytes("fuzz.yml", data)
	})
}

// FuzzFixBytes asserts three invariants on arbitrary input:
//  1. no panic;
//  2. the safety valve never fires (a produced fix always parses and always
//     removes its findings — valve firing is by definition a fix-generation
//     bug, even on garbage input);
//  3. fixing is idempotent: running --fix on already-fixed output applies
//     nothing further.
func FuzzFixBytes(f *testing.F) {
	fuzzSeeds(f)
	pm := map[string]string{"npm": "package-lock.json", "go": "go.sum", "pip": "requirements.txt"}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		out, res, err := FixBytes("fuzz.yml", data, pm, nil)
		if err != nil {
			t.Fatalf("safety valve fired (fix-generation bug): %v\ninput:\n%s", err, data)
		}
		if out == nil {
			return
		}
		if len(res.Applied) == 0 {
			t.Fatalf("changed output with empty Applied list\ninput:\n%s", data)
		}
		out2, _, err2 := FixBytes("fuzz.yml", out, pm, nil)
		if err2 != nil {
			t.Fatalf("second pass safety valve: %v\nfixed output:\n%s", err2, out)
		}
		if out2 != nil && !bytes.Equal(out2, out) {
			t.Fatalf("fix not idempotent\nfirst output:\n%s\nsecond output:\n%s", out, out2)
		}
	})
}

// FuzzLintActionBytes asserts the action-manifest linter never panics.
// Manifests come from strangers' repos over the git-trees + contents APIs,
// so this is attacker-reachable surface like FuzzLintBytes.
func FuzzLintActionBytes(f *testing.F) {
	fuzzSeeds(f)
	f.Add([]byte("name: x\nruns:\n  using: node20\n  main: dist/index.js\n"))
	f.Add([]byte("runs:\n  using: composite\n  steps:\n    - uses: actions/cache@v2\n    - run: echo \"::set-output name=a::b\"\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = LintActionBytes("action.yml", data)
	})
}
