package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFull(t *testing.T) {
	cfg, warns, err := Parse(".gha-doctor.yml", []byte(`
disable: [D004, d009]
runs: 150
cache-logs: 40
flaky-logs: 25
log-tail: 30
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !reflect.DeepEqual(cfg.Disable, []string{"D004", "D009"}) {
		t.Errorf("disable = %v", cfg.Disable)
	}
	if cfg.Runs == nil || *cfg.Runs != 150 {
		t.Errorf("runs = %v", cfg.Runs)
	}
	if cfg.CacheLogs == nil || *cfg.CacheLogs != 40 {
		t.Errorf("cache-logs = %v", cfg.CacheLogs)
	}
	if cfg.FlakyLogs == nil || *cfg.FlakyLogs != 25 {
		t.Errorf("flaky-logs = %v", cfg.FlakyLogs)
	}
	if cfg.LogTail == nil || *cfg.LogTail != 30 {
		t.Errorf("log-tail = %v", cfg.LogTail)
	}
	if got := cfg.Summary(); got != "disable D004, D009; runs 150; cache-logs 40; flaky-logs 25; log-tail 30" {
		t.Errorf("summary = %q", got)
	}
}

func TestParseDisableCommaString(t *testing.T) {
	cfg, warns, err := Parse("f", []byte(`disable: "D004, D009"`))
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if !reflect.DeepEqual(cfg.Disable, []string{"D004", "D009"}) {
		t.Errorf("disable = %v", cfg.Disable)
	}
}

func TestParseWarnings(t *testing.T) {
	cfg, warns, err := Parse("f", []byte(`
disable: [D004, D999]
runs: -5
log_tail: twenty
timeout: 3
`))
	if err != nil {
		t.Fatal(err)
	}
	// The good parts still apply.
	if !reflect.DeepEqual(cfg.Disable, []string{"D004"}) {
		t.Errorf("disable = %v", cfg.Disable)
	}
	if cfg.Runs != nil || cfg.LogTail != nil {
		t.Errorf("bad values should be skipped: runs=%v log-tail=%v", cfg.Runs, cfg.LogTail)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{`unknown rule ID "D999"`, "runs: must be >= 1", "log_tail: expected an integer", `unknown key "timeout"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q in:\n%s", want, joined)
		}
	}
}

func TestParseNotAMapping(t *testing.T) {
	if _, _, err := Parse("f", []byte(`- just\n- a list`)); err == nil {
		t.Fatal("expected error for non-mapping document")
	}
}

func TestParseEmpty(t *testing.T) {
	cfg, warns, err := Parse("f", nil)
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if cfg.Summary() != "no settings" {
		t.Errorf("summary = %q", cfg.Summary())
	}
}

func TestFindLocalPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, ".github", "gha-doctor.yml"), []byte("runs: 10"), 0o644)
	cfg, _, err := FindLocal(dir)
	if err != nil || cfg == nil || cfg.File != ".github/gha-doctor.yml" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	// Root file wins over .github/.
	os.WriteFile(filepath.Join(dir, ".gha-doctor.yml"), []byte("runs: 20"), 0o644)
	cfg, _, err = FindLocal(dir)
	if err != nil || cfg == nil || cfg.File != ".gha-doctor.yml" || *cfg.Runs != 20 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestFindLocalNone(t *testing.T) {
	cfg, warns, err := FindLocal(t.TempDir())
	if cfg != nil || warns != nil || err != nil {
		t.Fatalf("expected all nil, got %v %v %v", cfg, warns, err)
	}
}

func TestFindLocalBroken(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gha-doctor.yml"), []byte("disable: ["), 0o644)
	if _, _, err := FindLocal(dir); err == nil {
		t.Fatal("expected error for unparseable config")
	}
}

func TestPickRemote(t *testing.T) {
	if got := PickRemote([]string{"README.md"}, []string{"dependabot.yml"}); got != "" {
		t.Errorf("got %q", got)
	}
	if got := PickRemote(nil, []string{"gha-doctor.yaml"}); got != ".github/gha-doctor.yaml" {
		t.Errorf("got %q", got)
	}
	if got := PickRemote([]string{".gha-doctor.yaml"}, []string{"gha-doctor.yml"}); got != ".gha-doctor.yaml" {
		t.Errorf("got %q", got)
	}
}

func TestParseFailOn(t *testing.T) {
	cases := map[string]string{
		"any": FailAny, "info": FailAny,
		"warning": FailWarn, "warn": FailWarn, " Warning ": FailWarn,
		"never": FailNever, "none": FailNever, "NEVER": FailNever,
	}
	for in, want := range cases {
		got, err := ParseFailOn(in)
		if err != nil || got != want {
			t.Errorf("ParseFailOn(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "always", "error", "2"} {
		if _, err := ParseFailOn(in); err == nil {
			t.Errorf("ParseFailOn(%q): want error", in)
		}
	}
}

func TestParseFailOnKey(t *testing.T) {
	cfg, warns, err := Parse(".gha-doctor.yml", []byte("fail_on: Never\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if cfg.FailOn == nil || *cfg.FailOn != FailNever {
		t.Errorf("fail_on = %v, want never (normalized)", cfg.FailOn)
	}
	if got := cfg.Summary(); !strings.Contains(got, "fail-on never") {
		t.Errorf("Summary() = %q, want it to mention fail-on never", got)
	}

	// An invalid value must warn and leave FailOn unset — a typo cannot
	// silently weaken or tighten the CI gate.
	cfg, warns, err = Parse(".gha-doctor.yml", []byte("fail-on: always\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FailOn != nil {
		t.Errorf("invalid value must leave FailOn unset, got %q", *cfg.FailOn)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "fail-on") {
		t.Errorf("want one fail-on warning, got %v", warns)
	}
}

func TestParseMinScoreKey(t *testing.T) {
	cfg, warns, err := Parse(".gha-doctor.yml", []byte("min_score: 70\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if cfg.MinScore == nil || *cfg.MinScore != 70 {
		t.Errorf("min_score = %v, want 70", cfg.MinScore)
	}
	if got := cfg.Summary(); !strings.Contains(got, "min-score 70") {
		t.Errorf("Summary() = %q, want it to mention min-score 70", got)
	}

	// Out-of-range or non-integer values must warn and leave MinScore
	// unset — a typo'd gate must never silently (not) gate.
	for _, bad := range []string{"min-score: 101\n", "min-score: -1\n", "min-score: strict\n"} {
		cfg, warns, err = Parse(".gha-doctor.yml", []byte(bad))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MinScore != nil {
			t.Errorf("%q must leave MinScore unset, got %d", bad, *cfg.MinScore)
		}
		if len(warns) != 1 || !strings.Contains(warns[0], "min-score") {
			t.Errorf("%q: want one min-score warning, got %v", bad, warns)
		}
	}

	// Boundary values are legal: 0 (gate only a nothing-scored repo... or
	// really: always pass) and 100 (require a perfect score).
	for _, v := range []string{"min-score: 0\n", "min-score: 100\n"} {
		_, warns, err = Parse(".gha-doctor.yml", []byte(v))
		if err != nil || len(warns) != 0 {
			t.Errorf("%q: want no warnings, got %v, %v", v, warns, err)
		}
	}
}
