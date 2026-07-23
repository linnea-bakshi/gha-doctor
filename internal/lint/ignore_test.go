package lint

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIgnores(t *testing.T) {
	data := []byte(strings.Join([]string{
		"a: 1  # gha-doctor: ignore",           // line 1: bare = all rules
		"b: 2  # gha-doctor: ignore[D003]",     // line 2: one rule
		"c: 3  # gha-doctor:ignore[d003, D08]", // line 3: no space, lowercase, multiple
		"d: 4  # just a comment",               // line 4: nothing
		"# gha-doctor: ignore[D002]",           // line 5: comment-only line
	}, "\n"))
	ign := parseIgnores(data)

	cases := []struct {
		line int
		rule string
		want bool
	}{
		{1, "D001", true}, // bare matches anything
		{1, "D012", true},
		{2, "D003", true},
		{2, "D001", false}, // scoped: other rules unaffected
		{3, "D003", true},  // case-insensitive
		{3, "D08", true},
		{4, "D003", false},
		{5, "D002", true},
		{6, "D002", true}, // line below a comment-only directive
		{6, "D003", false},
		{7, "D002", false}, // two lines below: out of reach
	}
	for _, c := range cases {
		if got := ign.matches(c.line, c.rule); got != c.want {
			t.Errorf("matches(%d, %s) = %v, want %v", c.line, c.rule, got, c.want)
		}
	}
}

func TestInlineIgnoreSuppressesFinding(t *testing.T) {
	yml := `name: CI
on:
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # gha-doctor: ignore[D004]
`
	fs, err := LintBytes("ci.yml", []byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Rule == "D004" {
			t.Errorf("D004 should be suppressed by inline ignore, got: %s", f.Message)
		}
	}
	// D001 (no concurrency) must survive: the directive is scoped to D004.
	if !hasRule(fs, "D001") {
		t.Error("expected D001 to still be reported")
	}
}

func TestIgnoreOnLineAbove(t *testing.T) {
	yml := `name: CI
on:
  pull_request:
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
jobs:
  test:
    # gha-doctor: ignore[D002]
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`
	// Directive is above `runs-on`, not above the job key — D002 is reported
	// at the job key line, so this must NOT suppress it.
	fs, err := LintBytes("ci.yml", []byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(fs, "D002") {
		t.Error("directive above runs-on should not suppress D002 (reported at job key)")
	}

	yml2 := strings.Replace(yml,
		"  test:\n    # gha-doctor: ignore[D002]",
		"  # gha-doctor: ignore[D002]\n  test:", 1)
	fs2, err := LintBytes("ci.yml", []byte(yml2))
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(fs2, "D002") {
		t.Error("directive on line above job key should suppress D002")
	}
}

func TestFixRespectsInlineIgnore(t *testing.T) {
	yml := `name: CI
on:
  pull_request:
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
jobs:
  # gha-doctor: ignore[D002]
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4  # gha-doctor: ignore[D003]
        with:
          node-version: 20
      - run: npm ci
`
	root := writeRepo(t, map[string]string{"ci.yml": yml}, "package-lock.json")
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if len(r.Applied) != 0 {
			t.Errorf("expected no fixes applied, got %v", r.Applied)
		}
	}
	out := readWF(t, root, "ci.yml")
	if strings.Contains(out, "timeout-minutes") || strings.Contains(out, "cache: npm") {
		t.Errorf("ignored findings were fixed anyway:\n%s", out)
	}
}

func TestFixRespectsDisabledRules(t *testing.T) {
	root := writeRepo(t, map[string]string{"ci.yml": ciMissingAll}, "package-lock.json")
	results, err := FixDir(filepath.Join(root, ".github", "workflows"), root, []string{"d002", "D003"})
	if err != nil {
		t.Fatal(err)
	}
	out := readWF(t, root, "ci.yml")
	if strings.Contains(out, "timeout-minutes") || strings.Contains(out, "cache: npm") {
		t.Errorf("disabled rules were fixed anyway:\n%s", out)
	}
	// D001 is not disabled and must still be fixed.
	if !strings.Contains(out, "cancel-in-progress: true") {
		t.Errorf("D001 should still be fixed, got:\n%s", out)
	}
	var applied []string
	for _, r := range results {
		applied = append(applied, r.Applied...)
	}
	for _, a := range applied {
		if strings.HasPrefix(a, "D002") || strings.HasPrefix(a, "D003") {
			t.Errorf("disabled rule reported as applied: %s", a)
		}
	}
}

func hasRule(fs []Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}
