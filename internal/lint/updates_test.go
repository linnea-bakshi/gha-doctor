package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nf(path, data string) *NamedFile {
	return &NamedFile{Path: path, Data: []byte(data)}
}

func TestCheckUpdateAutomationMissingEverything(t *testing.T) {
	fs := CheckUpdateAutomation(nil, "")
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "D017" || f.Severity != Info || f.File != ".github/dependabot.yml" {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if !strings.Contains(f.Message, "no update automation") {
		t.Fatalf("message: %q", f.Message)
	}
}

func TestCheckUpdateAutomationRenovateCovers(t *testing.T) {
	if fs := CheckUpdateAutomation(nil, "renovate.json"); len(fs) != 0 {
		t.Fatalf("renovate config should silence D017, got %+v", fs)
	}
	// renovate wins even when a dependabot config exists without the
	// github-actions ecosystem
	db := nf(".github/dependabot.yml", "version: 2\nupdates:\n  - package-ecosystem: npm\n    directory: /\n")
	if fs := CheckUpdateAutomation(db, ".renovaterc"); len(fs) != 0 {
		t.Fatalf("renovate should cover, got %+v", fs)
	}
}

func TestCheckUpdateAutomationDependabotWithEcosystem(t *testing.T) {
	db := nf(".github/dependabot.yml", `version: 2
updates:
  - package-ecosystem: npm
    directory: /
  - package-ecosystem: "github-actions"
    directory: /
    schedule:
      interval: weekly
`)
	if fs := CheckUpdateAutomation(db, ""); len(fs) != 0 {
		t.Fatalf("github-actions ecosystem present, want no finding, got %+v", fs)
	}
}

func TestCheckUpdateAutomationDependabotMissingEcosystem(t *testing.T) {
	db := nf(".github/dependabot.yaml", `version: 2
updates:
  - package-ecosystem: gomod
    directory: /
`)
	fs := CheckUpdateAutomation(db, "")
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %d", len(fs))
	}
	f := fs[0]
	if f.Rule != "D017" || f.File != ".github/dependabot.yaml" {
		t.Fatalf("unexpected finding: %+v", f)
	}
	if f.Line != 2 { // the updates: key line
		t.Fatalf("want line 2 (updates:), got %d", f.Line)
	}
	if !strings.Contains(f.Message, "github-actions ecosystem") {
		t.Fatalf("message: %q", f.Message)
	}
}

func TestCheckUpdateAutomationUnparseableDependabotBenefitOfDoubt(t *testing.T) {
	db := nf(".github/dependabot.yml", "version: 2\n\tupdates: [")
	if fs := CheckUpdateAutomation(db, ""); len(fs) != 0 {
		t.Fatalf("unparseable config must not produce findings, got %+v", fs)
	}
}

func TestCheckUpdateAutomationInlineIgnore(t *testing.T) {
	db := nf(".github/dependabot.yml", `version: 2
# gha-doctor: ignore[D017]
updates:
  - package-ecosystem: pip
    directory: /
`)
	if fs := CheckUpdateAutomation(db, ""); len(fs) != 0 {
		t.Fatalf("inline ignore should silence, got %+v", fs)
	}
}

func TestFindUpdateConfigLocal(t *testing.T) {
	root := t.TempDir()
	if db, ren := FindUpdateConfigLocal(root); db != nil || ren != "" {
		t.Fatalf("empty repo: got %+v %q", db, ren)
	}

	if err := os.MkdirAll(filepath.Join(root, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "version: 2\nupdates: []\n"
	if err := os.WriteFile(filepath.Join(root, ".github", "dependabot.yml"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	db, ren := FindUpdateConfigLocal(root)
	if db == nil || db.Path != ".github/dependabot.yml" || string(db.Data) != want || ren != "" {
		t.Fatalf("got %+v %q", db, ren)
	}

	if err := os.WriteFile(filepath.Join(root, ".renovaterc.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ren := FindUpdateConfigLocal(root); ren != ".renovaterc.json" {
		t.Fatalf("renovate path: %q", ren)
	}

	// a directory named renovate.json must not count
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, "renovate.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ren := FindUpdateConfigLocal(root2); ren != "" {
		t.Fatalf("dir named renovate.json counted: %q", ren)
	}
}
