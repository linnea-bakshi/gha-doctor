package completion

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func testFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("gha-doctor", flag.ContinueOnError)
	fs.String("repo", "", "owner/name to analyze (default: detect from git remote)")
	fs.String("dir", ".", "repository directory to scan")
	fs.String("explain", "", "print the documentation for a rule and exit, e.g. --explain D004")
	fs.String("disable", "", "comma-separated rule IDs to disable, e.g. D004,D009 (inline: # gha-doctor: ignore[D004])")
	fs.String("completion", "", "print a shell completion script and exit (bash, zsh, or fish)")
	fs.Bool("json", false, "output JSON")
	fs.Int("runs", 100, "number of recent runs to sample")
	return fs
}

func TestScriptCoversEveryFlag(t *testing.T) {
	fs := testFlagSet()
	for _, shell := range Shells {
		var buf bytes.Buffer
		if err := Script(&buf, shell, fs); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		out := buf.String()
		fs.VisitAll(func(f *flag.Flag) {
			marker := "--" + f.Name
			if shell == "fish" {
				marker = "-l " + f.Name
			}
			if !strings.Contains(out, marker) {
				t.Errorf("%s script missing flag %q", shell, f.Name)
			}
		})
	}
}

func TestScriptValueCompletion(t *testing.T) {
	fs := testFlagSet()
	for _, shell := range Shells {
		var buf bytes.Buffer
		if err := Script(&buf, shell, fs); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		for _, want := range []string{"D001", "D012", "bash zsh fish"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s script missing value candidates %q", shell, want)
			}
		}
	}
}

func TestBoolFlagsTakeNoArgument(t *testing.T) {
	fs := testFlagSet()
	var buf bytes.Buffer
	if err := Script(&buf, "fish", fs); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "-l json") && (strings.Contains(line, " -r") || strings.Contains(line, " -x")) {
			t.Errorf("bool flag --json should not require an argument: %s", line)
		}
	}
	buf.Reset()
	if err := Script(&buf, "zsh", fs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "'--json[output JSON]'") {
		t.Errorf("zsh: bool flag --json should have a no-argument spec")
	}
}

func TestZshDescEscaping(t *testing.T) {
	fs := testFlagSet()
	var buf bytes.Buffer
	if err := Script(&buf, "zsh", fs); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// The --disable usage contains ":" and "[]"; both are structural in
	// _arguments specs and must be escaped/replaced inside descriptions.
	if strings.Contains(out, "ignore[D004]") {
		t.Error("zsh description leaked unescaped square brackets")
	}
	if !strings.Contains(out, `inline\:`) {
		t.Error("zsh description colon not escaped")
	}
}

func TestUnknownShell(t *testing.T) {
	var buf bytes.Buffer
	if err := Script(&buf, "powershell", testFlagSet()); err == nil {
		t.Fatal("expected error for unsupported shell")
	}
}

// TestBashSyntax runs `bash -n` on the generated script when bash exists.
func TestBashSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	var buf bytes.Buffer
	if err := Script(&buf, "bash", testFlagSet()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = &buf
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, out)
	}
}

// TestShellSyntaxIfInstalled syntax-checks zsh and fish scripts when those
// shells are present (they are on CI runners).
func TestShellSyntaxIfInstalled(t *testing.T) {
	cases := []struct {
		shell string
		args  []string
	}{
		{"zsh", []string{"-n"}},
		{"fish", []string{"--no-execute"}},
	}
	for _, c := range cases {
		path, err := exec.LookPath(c.shell)
		if err != nil {
			t.Logf("%s not installed; skipping", c.shell)
			continue
		}
		var buf bytes.Buffer
		if err := Script(&buf, c.shell, testFlagSet()); err != nil {
			t.Fatal(err)
		}
		f := t.TempDir() + "/script"
		if err := writeFile(f, buf.Bytes()); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(path, append(c.args, f)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s syntax check failed: %v\n%s", c.shell, err, out)
		}
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
