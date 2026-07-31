package lint

import (
	"path/filepath"
	"strings"
	"testing"
)

// PreviewDir must (1) write nothing, and (2) predict exactly what FixDir
// then does to the same tree.
func TestPreviewDirMatchesFixDirAndDoesNotWrite(t *testing.T) {
	workflows := map[string]string{
		"ci.yml": ciMissingAll,
		"cache.yml": `name: cache
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v2
        with:
          path: ~/.npm
          key: npm-${{ hashFiles('package-lock.json') }}
`,
	}
	root := writeRepo(t, workflows, "package-lock.json")
	wfDir := filepath.Join(root, ".github", "workflows")

	before := map[string]string{}
	for name := range workflows {
		before[name] = readWF(t, root, name)
	}

	previews, err := PreviewDir(wfDir, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) == 0 {
		t.Fatal("expected previews")
	}
	for name := range workflows {
		if got := readWF(t, root, name); got != before[name] {
			t.Errorf("PreviewDir modified %s", name)
		}
	}

	fixed := map[string][]byte{}
	applied := 0
	for _, p := range previews {
		if p.Fixed != nil {
			fixed[filepath.Base(p.Path)] = p.Fixed
		}
		applied += len(p.Applied)
	}
	if applied == 0 {
		t.Fatal("expected at least one applied fix in preview")
	}

	if _, err := FixDir(wfDir, root, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range fixed {
		if got := readWF(t, root, name); got != string(want) {
			t.Errorf("preview for %s does not match what --fix wrote", name)
		}
	}
}

func TestPreviewFilesRemote(t *testing.T) {
	pm := DetectPackageManagersFromList([]string{"README.md", "package-lock.json", "LICENSE"})
	if pm["node"] != "npm" {
		t.Fatalf("expected node=npm from listing, got %v", pm)
	}
	files := []NamedFile{{Path: ".github/workflows/ci.yml", Data: []byte(`name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
      - run: npm install
`)}}
	previews := PreviewFiles(files, pm, nil)
	if len(previews) != 1 {
		t.Fatalf("expected 1 preview, got %d", len(previews))
	}
	p := previews[0]
	if p.Fixed == nil {
		t.Fatal("expected a fix preview")
	}
	out := string(p.Fixed)
	if !strings.Contains(out, "cache: npm") {
		t.Errorf("expected D003 fix using remote-detected npm, got:\n%s", out)
	}
	if !strings.Contains(out, "npm ci") {
		t.Errorf("expected D012 npm ci fix, got:\n%s", out)
	}
	d := UnifiedDiff("a/"+p.Path, "b/"+p.Path, string(p.Original), string(p.Fixed), 3)
	if !strings.Contains(d, "+      - run: npm ci") {
		t.Errorf("diff missing npm ci insertion:\n%s", d)
	}
}

func TestDetectPackageManagersFromListAmbiguous(t *testing.T) {
	pm := DetectPackageManagersFromList([]string{"yarn.lock", "package-lock.json"})
	if _, ok := pm["node"]; ok {
		t.Errorf("ambiguous lockfiles must not pick a package manager, got %v", pm)
	}
}

func TestPreviewFilesRespectsDisable(t *testing.T) {
	files := []NamedFile{{Path: "wf.yml", Data: []byte(`name: x
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)}}
	previews := PreviewFiles(files, nil, []string{"D001", "D002"})
	for _, p := range previews {
		if p.Fixed != nil {
			t.Errorf("disabled rules still produced a fix:\n%s", p.Fixed)
		}
	}
}
