package lint

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRuleDeprecatedCommand(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: |
          echo "::set-output name=sha::abc"
          echo "::set-env name=FOO::bar"
          # echo "::add-path::/commented/out"
  w:
    runs-on: windows-latest
    timeout-minutes: 10
    steps:
      - run: Write-Output "::set-output name=x::y"
`
	fs, err := LintBytes("ci.yml", []byte(wf))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		if f.Rule == "D018" {
			if f.Severity != Warn {
				t.Fatalf("D018 should be warn, got %v", f.Severity)
			}
			got = append(got, f.Message)
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 D018 findings (set-output+set-env in a, set-output in w), got %d: %v", len(got), got)
	}
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "add-path") {
		t.Fatalf("comment-only occurrence must not be flagged: %v", got)
	}
	if !strings.Contains(joined, "::set-env") || !strings.Contains(joined, "errors at runtime") {
		t.Fatalf("set-env message should say it errors at runtime: %v", got)
	}
	if !strings.Contains(joined, "::set-output") || !strings.Contains(joined, "deprecation warning") {
		t.Fatalf("set-output message should mention the deprecation warning: %v", got)
	}
}

func TestFixDeprecatedCommands(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: |
          echo "::set-output name=version::1.2.3"
          echo ::add-path::/opt/tools/bin
          echo '::set-env name=FOO::bar baz'
      - run: echo "::save-state name=st::yes"
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	for _, want := range []string{
		`echo "version=1.2.3" >> "$GITHUB_OUTPUT"`,
		`echo /opt/tools/bin >> "$GITHUB_PATH"`,
		`echo 'FOO=bar baz' >> "$GITHUB_ENV"`,
		`- run: echo "st=yes" >> "$GITHUB_STATE"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing rewrite %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "::set-output") || strings.Contains(out, "::add-path") ||
		strings.Contains(out, "::set-env") || strings.Contains(out, "::save-state") {
		t.Fatalf("deprecated commands should be gone:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Applied) != 4 {
		t.Fatalf("expected 4 applied D018 fixes, got %+v", results)
	}
	after, _ := LintBytes("ci.yml", []byte(out))
	if countRule(after, "D018") != 0 {
		t.Fatalf("D018 still present after fix:\n%s", out)
	}
	// Idempotency: a second pass changes nothing.
	out2, res2, err := FixBytes("ci.yml", []byte(out), nil, nil)
	if err != nil || out2 != nil || len(res2.Applied) != 0 {
		t.Fatalf("second pass should be a no-op, got out=%v res=%+v err=%v", out2 != nil, res2, err)
	}
}

func TestFixDeprecatedCommandsSkips(t *testing.T) {
	wf := `name: CI
on:
  push:
jobs:
  win:
    runs-on: windows-latest
    timeout-minutes: 10
    steps:
      - run: echo "::set-output name=a::b"
  pwsh:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - shell: pwsh
        run: echo "::set-output name=a::b"
  escapes:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: echo "::set-output name=log::line1%0Aline2"
  partial:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: |
          echo "::set-output name=ok::yes"
          printf '::set-output name=no::%s' "$VAL"
  unknownrunner:
    runs-on: ${{ inputs.runner }}
    timeout-minutes: 10
    steps:
      - run: echo "::set-output name=a::b"
  matrixwin:
    runs-on: ${{ matrix.os }}
    timeout-minutes: 10
    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest]
    steps:
      - run: echo "::set-output name=a::b"
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if out != wf {
		t.Fatalf("nothing should have been rewritten:\n%s", out)
	}
	if len(results) != 1 || len(results[0].Applied) != 0 {
		t.Fatalf("expected no applied fixes, got %+v", results)
	}
	notes := strings.Join(results[0].Skipped, "\n")
	for _, want := range []string{"Windows", "pwsh", "partial", "runner", "matrixwin", "escapes"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("expected a skip note mentioning %q, got:\n%s", want, notes)
		}
	}
	if len(results[0].Skipped) != 6 {
		t.Fatalf("expected 6 skip notes, got %d:\n%s", len(results[0].Skipped), notes)
	}
}

func TestFixDeprecatedCommandsAdjacentSteps(t *testing.T) {
	// A block-scalar step directly followed by a plain-scalar step using the
	// same command: the span scan for step one must not swallow or
	// double-count step two's line.
	wf := `name: CI
on:
  push:
jobs:
  a:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - run: |
          echo "::set-output name=a::b"
      - run: echo "::set-output name=c::d"
`
	root := writeRepo(t, map[string]string{"ci.yml": wf})
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if strings.Contains(out, "::set-output") {
		t.Fatalf("both steps should be rewritten:\n%s", out)
	}
	if !strings.Contains(out, `echo "a=b" >> "$GITHUB_OUTPUT"`) ||
		!strings.Contains(out, `- run: echo "c=d" >> "$GITHUB_OUTPUT"`) {
		t.Fatalf("rewrites missing:\n%s", out)
	}
	if len(results[0].Skipped) != 0 {
		t.Fatalf("no skips expected, got %v", results[0].Skipped)
	}
}

func TestParseDeprecatedEchoRejects(t *testing.T) {
	for _, line := range []string{
		`echo "::set-output name=a::b" && make`,
		`echo "::set-output name=a::b" # note`,
		`echo "::set-output name=bad key::v"`,
		`echo "::set-output name=a::has "quote""`,
		`echo "::add-path::"`,
		`FOO=1 echo "::set-output name=a::b"`,
		`# echo "::set-output name=a::b"`,
		`echo -n "::set-output name=a::b"`,
	} {
		if got, _, ok := parseDeprecatedEcho(line); ok {
			t.Fatalf("line %q should not parse, got %q", line, got)
		}
	}
}
