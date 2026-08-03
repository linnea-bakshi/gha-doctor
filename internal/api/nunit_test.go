package api

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

// Real NUnit3 report uploaded by Unity's test runner (excerpt of the
// EditMode results artifact from unitystation/unitystation run
// 30821876456: whole elements removed to keep the fixture small, kept
// bytes unmodified). Suites at every level carry result="Failed" and
// their own <failure> blocks — only the one failing test-case may be
// named, once.
func TestParseNUnit3RealUnityFailing(t *testing.T) {
	data, err := os.ReadFile("testdata/nunit/unity-editmode-failing.xml")
	if err != nil {
		t.Fatal(err)
	}
	fails, cases, ok := parseNUnit3(data)
	if !ok {
		t.Fatal("real Unity NUnit3 report not recognized")
	}
	if cases != 7 {
		t.Fatalf("cases = %d, want 7", cases)
	}
	want := "Tests.ScanCode.ScanCodeReport"
	if len(fails) != 1 || fails[0] != want {
		t.Fatalf("failures = %q, want [%q]", fails, want)
	}
	// The same document must not be claimed by the other two parsers.
	if _, _, jok := parseJUnitXML(data); jok {
		t.Fatal("JUnit parser claimed an NUnit3 document")
	}
	if _, _, tok := parseTRX(data); tok {
		t.Fatal("TRX parser claimed an NUnit3 document")
	}
}

// Real NUnit3 report written by `dotnet test --logger nunit`
// (NunitXml.TestLogger) on a GitHub runner: one passing (self-closing
// element — the skip-children path must survive it), one failing.
func TestParseNUnit3RealTestLoggerFailing(t *testing.T) {
	data, err := os.ReadFile("testdata/nunit/testlogger-failing.xml")
	if err != nil {
		t.Fatal(err)
	}
	fails, cases, ok := parseNUnit3(data)
	if !ok {
		t.Fatal("real NunitXml.TestLogger report not recognized")
	}
	if cases != 2 {
		t.Fatalf("cases = %d, want 2", cases)
	}
	want := "Payments.NUnitTests.PaymentsNUnitSuite.Refund_Partial_ReturnsOk"
	if len(fails) != 1 || fails[0] != want {
		t.Fatalf("failures = %q, want [%q]", fails, want)
	}
}

func TestParseNUnit3Synthetic(t *testing.T) {
	cases := []struct {
		name      string
		doc       string
		wantOK    bool
		wantFails []string
		wantCases int
	}{
		{
			name: "Error and Invalid labels count; Cancelled does not",
			doc: `<test-run testcasecount="4">
  <test-suite name="s" result="Failed">
    <test-case fullname="T.err" result="Failed" label="Error"/>
    <test-case fullname="T.bad" result="Failed" label="Invalid"/>
    <test-case fullname="T.stop" result="Failed" label="Cancelled"/>
    <test-case fullname="T.ok" result="Passed"/>
  </test-suite>
</test-run>`,
			wantOK:    true,
			wantFails: []string{"T.err", "T.bad"},
			wantCases: 4,
		},
		{
			name: "Skipped and Inconclusive are not failures; name falls back when fullname absent",
			doc: `<test-run testcasecount="3">
  <test-case name="bare_fail" result="Failed"/>
  <test-case fullname="T.skip" result="Skipped" label="Ignored"/>
  <test-case fullname="T.meh" result="Inconclusive"/>
</test-run>`,
			wantOK:    true,
			wantFails: []string{"bare_fail"},
			wantCases: 3,
		},
		{
			name:   "root without testcasecount fingerprint rejected",
			doc:    `<test-run><test-case fullname="T.x" result="Failed"/></test-run>`,
			wantOK: false,
		},
		{
			name:   "namespaced test-run rejected",
			doc:    `<test-run xmlns="http://example.com/x" testcasecount="1"/>`,
			wantOK: false,
		},
		{
			name:   "wrong root rejected",
			doc:    `<testsuites tests="1" testcasecount="1"/>`,
			wantOK: false,
		},
		{
			name:   "not XML rejected",
			doc:    `just some text`,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fails, n, ok := parseNUnit3([]byte(tc.doc))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if n != tc.wantCases {
				t.Fatalf("cases = %d, want %d", n, tc.wantCases)
			}
			if strings.Join(fails, "|") != strings.Join(tc.wantFails, "|") {
				t.Fatalf("failures = %q, want %q", fails, tc.wantFails)
			}
		})
	}
}

// An NUnit3 report saved as .xml inside an artifact zip (the only way the
// format ships — Unity names them editmode-results.xml etc.) is scanned
// alongside JUnit files, and its failures merge into one deduped list.
func TestScanZipFindsNUnit3(t *testing.T) {
	nunit, err := os.ReadFile("testdata/nunit/testlogger-failing.xml")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("editmode-results.xml")
	w.Write(nunit)
	w2, _ := zw.Create("junit.xml")
	w2.Write([]byte(`<testsuite name="s"><testcase classname="C" name="t_ok"/></testsuite>`))
	zw.Close()
	fails, cases, files := scanJUnitZip(buf.Bytes())
	if files != 2 {
		t.Fatalf("report files = %d, want 2", files)
	}
	if cases != 3 {
		t.Fatalf("cases = %d, want 3", cases)
	}
	want := "Payments.NUnitTests.PaymentsNUnitSuite.Refund_Partial_ReturnsOk"
	if len(fails) != 1 || fails[0] != want {
		t.Fatalf("failures = %q, want [%q]", fails, want)
	}
}

// nunit-named artifacts rank as high as junit/trx ones.
func TestNUnitArtifactScore(t *testing.T) {
	if got := junitArtifactScore("nunit-results"); got != 3 {
		t.Fatalf("junitArtifactScore(nunit-results) = %d, want 3", got)
	}
}
