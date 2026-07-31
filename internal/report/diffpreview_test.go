package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func samplePreviews() []lint.FixPreview {
	return []lint.FixPreview{
		{
			FixResult: lint.FixResult{
				Path:    ".github/workflows/ci.yml",
				Applied: []string{"D002: timeout-minutes: 30 on job `build`"},
				Skipped: []string{"D003: can't tell which package manager"},
			},
			Original: []byte("a\nb\n"),
			Fixed:    []byte("a\nX\nb\n"),
		},
		{
			FixResult: lint.FixResult{Path: ".github/workflows/broken.yml", Failed: "unreadable"},
		},
	}
}

func TestDiffPreviewTerminal(t *testing.T) {
	var buf bytes.Buffer
	DiffPreview(&buf, Style{Plain: true}, samplePreviews(), "")
	out := buf.String()
	for _, want := range []string{
		"--- a/.github/workflows/ci.yml",
		"+++ b/.github/workflows/ci.yml",
		"+X",
		"skip .github/workflows/ci.yml",
		"fail .github/workflows/broken.yml",
		"1 fix available in 1 file",
		"apply with --fix",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPreviewTerminalRemote(t *testing.T) {
	var buf bytes.Buffer
	DiffPreview(&buf, Style{Plain: true}, samplePreviews(), "psf/requests")
	if !strings.Contains(buf.String(), "inside a checkout of psf/requests") {
		t.Errorf("remote summary should not suggest bare --fix:\n%s", buf.String())
	}
}

func TestDiffPreviewNothingToFix(t *testing.T) {
	var buf bytes.Buffer
	DiffPreview(&buf, Style{Plain: true}, nil, "")
	if !strings.Contains(buf.String(), "nothing to fix") {
		t.Errorf("expected nothing-to-fix message:\n%s", buf.String())
	}
}

func TestDiffPreviewMD(t *testing.T) {
	var buf bytes.Buffer
	DiffPreviewMD(&buf, samplePreviews(), "")
	out := buf.String()
	for _, want := range []string{"## Autofix preview", "```diff", "+X", "- skip `", "1 fix available in 1 file"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}

func TestDiffPreviewJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := DiffPreviewJSON(&buf, samplePreviews()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		FixPreview []struct {
			Path   string `json:"path"`
			Diff   string `json:"diff"`
			Failed string `json:"failed"`
		} `json:"fix_preview"`
		FixesAvailable int `json:"fixes_available"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if doc.FixesAvailable != 1 || len(doc.FixPreview) != 2 {
		t.Fatalf("unexpected counts: %+v", doc)
	}
	if !strings.Contains(doc.FixPreview[0].Diff, "@@") {
		t.Error("first entry should carry a diff")
	}
	if doc.FixPreview[1].Failed != "unreadable" {
		t.Error("failed entry should carry its reason")
	}
}
