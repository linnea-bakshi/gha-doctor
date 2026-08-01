package lint

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func actionRules(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func TestActionRuntimeDeprecated(t *testing.T) {
	cases := []struct {
		using string
		want  bool
	}{
		{"node12", true}, {"node16", true}, {"node20", true},
		{"Node20", true}, // GitHub matches case-insensitively
		{"node24", false}, {"docker", false}, {"composite", false},
	}
	for _, c := range cases {
		src := "name: x\nruns:\n  using: " + c.using + "\n  main: dist/index.js\n"
		fs, ok := LintActionBytes("action.yml", []byte(src))
		if !ok {
			t.Fatalf("using=%s: not recognized as action", c.using)
		}
		got := len(fs) == 1 && fs[0].Rule == "D019"
		if got != c.want {
			t.Errorf("using=%s: findings=%v want D019=%v", c.using, actionRules(fs), c.want)
		}
		if c.want && fs[0].Line != 3 {
			t.Errorf("using=%s: line=%d want 3", c.using, fs[0].Line)
		}
	}
}

func TestActionCompositeStepsGetD015AndD018(t *testing.T) {
	src := `name: x
runs:
  using: composite
  steps:
    - uses: actions/cache@v2
      with: {path: ~/.npm, key: npm}
    - run: echo "::set-output name=a::b"
      shell: bash
    - uses: actions/checkout@v4
`
	fs, ok := LintActionBytes("action.yml", []byte(src))
	if !ok {
		t.Fatal("not recognized as action")
	}
	want := []string{"D015", "D018"}
	if !reflect.DeepEqual(actionRules(fs), want) {
		t.Fatalf("rules=%v want %v", actionRules(fs), want)
	}
}

func TestActionInlineIgnore(t *testing.T) {
	src := "runs:\n  using: node16 # gha-doctor: ignore[D019]\n  main: index.js\n"
	fs, ok := LintActionBytes("action.yml", []byte(src))
	if !ok || len(fs) != 0 {
		t.Fatalf("ok=%v findings=%v; want recognized and suppressed", ok, fs)
	}
}

func TestActionFileNotAManifest(t *testing.T) {
	for _, src := range []string{
		"a: [broken\n",             // parse failure
		"name: config\nfoo: bar\n", // no runs mapping
		"runs: just-a-string\n",    // runs not a mapping
		"- a\n- b\n",               // sequence root
		"",                         // empty
	} {
		if fs, ok := LintActionBytes("action.yml", []byte(src)); ok || fs != nil {
			t.Errorf("src=%q: ok=%v findings=%v; want silently skipped", src, ok, fs)
		}
	}
}

func TestIsActionPath(t *testing.T) {
	cases := []struct {
		p    string
		want bool
	}{
		{"action.yml", true},
		{"action.yaml", true},
		{"restore/action.yml", true},
		{"actions/setup/action.yml", true},
		{"a/b/c/action.yml", false}, // beyond maxActionDepth
		{".github/actions/lint/action.yml", true},
		{".github/actions/a/b/action.yml", true},
		{".github/workflows/action.yml", false},
		{".github/action.yml", false},
		{"node_modules/pkg/action.yml", false},
		{"vendor/x/action.yml", false},
		{"dist/action.yml", false},
		{"testdata/action.yml", false},
		{".hidden/action.yml", false},
		{"sub/.hidden/action.yml", false},
		{"workflow.yml", false},
		{"some/other.yml", false},
	}
	for _, c := range cases {
		if got := IsActionPath(c.p); got != c.want {
			t.Errorf("IsActionPath(%q)=%v want %v", c.p, got, c.want)
		}
	}
}

func TestDiscoverActionFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(p string) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("runs:\n  using: node20\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{
		"action.yml",
		"restore/action.yml",
		".github/actions/lint/action.yml",
		".github/workflows/ci.yml",
		"node_modules/dep/action.yml",
		"deep/a/b/action.yml",
		"testdata/action.yml",
	} {
		write(p)
	}
	got, trunc := DiscoverActionFiles(dir)
	want := []string{".github/actions/lint/action.yml", "action.yml", "restore/action.yml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if trunc {
		t.Fatal("unexpected truncation")
	}

	files := ReadActionFiles(dir, got)
	fs, n := LintActionFiles(files)
	if n != 3 || len(fs) != 3 {
		t.Fatalf("scanned=%d findings=%d; want 3 and 3", n, len(fs))
	}
	for _, f := range fs {
		if f.Rule != "D019" {
			t.Errorf("unexpected rule %s in %s", f.Rule, f.File)
		}
	}
}
