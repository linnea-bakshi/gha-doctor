package report

import (
	"strings"
	"testing"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

func TestHTMLPageShell(t *testing.T) {
	page := string(HTMLPage("## Hello\n\nworld\n", HTMLMeta{
		Title:    "gha-doctor — a/<b>",
		Subtitle: "generated now",
		Grade:    "A+",
		Points:   98,
	}))
	for _, want := range []string{
		"<!DOCTYPE html>",
		"<title>gha-doctor — a/&lt;b&gt;</title>",
		"A+ · 98/100",
		`class="grade"`,
		"generated now",
		"<h2>Hello</h2>",
		"<p>world</p>",
		"https://github.com/linnea-bakshi/gha-doctor",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Grade chip uses the badge color for the grade.
	if !strings.Contains(page, badgeColor("A+")) {
		t.Errorf("grade chip missing badge color %s", badgeColor("A+"))
	}
	if strings.Contains(page, "<script") {
		t.Error("page must not contain scripts")
	}
}

func TestHTMLPageNoGradeNoChip(t *testing.T) {
	page := string(HTMLPage("hi\n", HTMLMeta{Title: "t"}))
	if strings.Contains(page, `class="grade"`) {
		t.Error("no grade given, but chip rendered")
	}
}

func TestHTMLTable(t *testing.T) {
	md := "| rule | severity | message |\n" +
		"|---|---|---|\n" +
		"| D001 | warn | pipe \\| kept |\n" +
		"| D999 | info | `snake_case` |\n"
	var b strings.Builder
	writeHTMLBody(&b, md)
	out := b.String()
	for _, want := range []string{
		"<table>", "<th>rule</th>",
		`<td class="sev-warn">warn</td>`,
		`<td class="sev-info">info</td>`,
		"<td>pipe | kept</td>",
		"<code>snake_case</code>", // underscores inside code untouched
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "---") {
		t.Error("separator row leaked into output")
	}
	// D001 exists -> linked to its docs anchor; D999 doesn't -> plain text.
	if !strings.Contains(out, "rules#d001-") {
		t.Error("known rule ID not linked")
	}
	if strings.Contains(out, "rules#d999") {
		t.Error("unknown rule ID must not be linked")
	}
}

func TestHTMLInline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**bold** move", "<strong>bold</strong> move"},
		{"see _the basis_.", "see <em>the basis</em>."},
		{"job build_all failed", "job build_all failed"}, // mid-word _ is not italics
		{"[View](https://github.com/x)", `<a href="https://github.com/x">View</a>`},
		{"[nope](javascript:alert(1))", "[nope](javascript:alert(1))"}, // non-http scheme stays literal
		{"`a ** b`", "<code>a ** b</code>"},                            // bold not applied inside code
		{"<script>x</script>", "&lt;script&gt;x&lt;/script&gt;"},
	}
	for _, c := range cases {
		if got := htmlInline(c.in); !strings.Contains(got, c.want) {
			t.Errorf("htmlInline(%q) = %q, want containing %q", c.in, got, c.want)
		}
	}
}

func TestHTMLFencedCodeEscapes(t *testing.T) {
	md := "### Log\n\n```text\nerror: <b>&\n**not bold**\n```\n"
	var b strings.Builder
	writeHTMLBody(&b, md)
	out := b.String()
	if !strings.Contains(out, "error: &lt;b&gt;&amp;") {
		t.Errorf("fence content not escaped: %s", out)
	}
	if strings.Contains(out, "<strong>") {
		t.Error("inline formatting applied inside fence")
	}
}

func TestHTMLBlockquoteAndList(t *testing.T) {
	md := "> verdict one\n> verdict two\n\n1. **First win** — do it\n2. Second\n"
	var b strings.Builder
	writeHTMLBody(&b, md)
	out := b.String()
	for _, want := range []string{
		"<blockquote>", "<p>verdict one</p>", "<p>verdict two</p>",
		"<ol>", "<li><strong>First win</strong> — do it</li>", "<li>Second</li>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The full Markdown report for a small findings set converts without leaking
// raw markdown syntax.
func TestHTMLEndToEnd(t *testing.T) {
	findings := []lint.Finding{
		{Rule: "D002", Severity: lint.Warn, File: "ci.yml", Line: 3,
			Message: "job `build` has no timeout-minutes"},
		{Rule: "D014", Severity: lint.Info, File: "cron.yml", Line: 1,
			Message: "cron fires at minute 0"},
	}
	var md strings.Builder
	Markdown(&md, findings, 2, nil, nil, nil, nil)
	page := string(HTMLPage(md.String(), HTMLMeta{Title: "t"}))
	for _, want := range []string{
		"<h2>gha-doctor report</h2>",
		`<td class="sev-warn">warning</td>`,
		"<code>build</code>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, leak := range []string{"**", "|---|", "### "} {
		if strings.Contains(page, leak) {
			t.Errorf("raw markdown %q leaked into HTML", leak)
		}
	}
}

func TestHTMLUnorderedList(t *testing.T) {
	md := "## Report\n\n- [Nightly](https://x.test/1) — 12 failures\n- second item\n\nafter.\n"
	var b strings.Builder
	writeHTMLBody(&b, md)
	out := b.String()
	for _, want := range []string{"<ul>", "<li><a href=\"https://x.test/1\">Nightly</a> — 12 failures</li>", "<li>second item</li>", "</ul>", "<p>after.</p>"} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q:\n%s", want, out)
		}
	}
}
