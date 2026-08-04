package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// testdata/testng/testng-results-excerpt.xml is a verbatim excerpt of the
// 35MB testng-results.xml inside artifact "test-reports-PGSQL" of failed
// JanssenProject/jans run 30735929014 (integration-tests, 2026-08-02):
// the real root element and suite line, plus three real <test> blocks —
// JsonApplier (all PASS), Select Account (a FAIL alongside config methods
// and RetryAnalyzer SKIP-retried attempts, with CDATA stack traces) and
// the AuthorizationResponseCustomHeaderTest class (seven FAIL entries
// sharing one method name across re-invocations).
//
// Ground truth below was computed independently from the same excerpt.
func TestParseTestNGRealExcerpt(t *testing.T) {
	data, err := os.ReadFile("testdata/testng/testng-results-excerpt.xml")
	if err != nil {
		t.Fatal(err)
	}
	fails, cases, ok := parseTestNG(data)
	if !ok {
		t.Fatal("real testng-results.xml excerpt not recognized")
	}
	// 11 executed test methods: config methods (is-config="true") and
	// RetryAnalyzer attempts (retried="true", 24 SKIPs in the excerpt)
	// are not tests.
	if cases != 11 {
		t.Errorf("cases = %d, want 11", cases)
	}
	// 8 FAIL entries, duplicates preserved (parser contract — the zip
	// scanner dedupes): 1 SelectAccountHttpTest + 7 re-invocations of
	// requestAuthorizationCustomHeader.
	if len(fails) != 8 {
		t.Fatalf("failures = %d, want 8: %v", len(fails), fails)
	}
	if fails[0] != "io.jans.as.client.ws.rs.SelectAccountHttpTest.selectAccountTest" {
		t.Errorf("first failure = %q", fails[0])
	}
	uniq := map[string]bool{}
	for _, f := range fails {
		uniq[f] = true
	}
	if len(uniq) != 2 || !uniq["io.jans.as.client.ws.rs.AuthorizationResponseCustomHeaderTest.requestAuthorizationCustomHeader"] {
		t.Errorf("unique failures = %v", uniq)
	}
}

// testng-failed-suite.xml is the (trimmed, still well-formed) rerun SUITE
// definition TestNG writes next to its results — root <suite>. It lists
// tests to re-run, not outcomes, and must be rejected by every parser in
// the fallback chain.
func TestParseTestNGRejectsSuiteDoc(t *testing.T) {
	data, err := os.ReadFile("testdata/testng/testng-failed-suite.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := parseTestNG(data); ok {
		t.Error("testng-failed.xml (rerun suite doc) parsed as results")
	}
	if _, _, ok := parseJUnitXML(data); ok {
		t.Error("testng-failed.xml parsed as JUnit")
	}
}

// A JUnit document must not be claimed by the TestNG parser (wrong root).
func TestParseTestNGRejectsJUnit(t *testing.T) {
	doc := []byte(`<testsuite name="s" tests="1"><testcase classname="C" name="t"><failure/></testcase></testsuite>`)
	if _, _, ok := parseTestNG(doc); ok {
		t.Error("JUnit doc parsed as TestNG")
	}
}

// The zip scanner reaches TestNG results through the format fallback
// chain and dedupes the re-invocation duplicates.
func TestScanZipTestNG(t *testing.T) {
	data, err := os.ReadFile("testdata/testng/testng-results-excerpt.xml")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("surefire-reports/testng-results.xml")
	w.Write(data)
	zw.Close()
	fails, cases, files, truncated := scanJUnitZip(buf.Bytes())
	if files != 1 || cases != 11 {
		t.Errorf("files=%d cases=%d, want 1/11", files, cases)
	}
	if len(fails) != 2 {
		t.Errorf("deduped failures = %v, want 2", fails)
	}
	if truncated {
		t.Error("single-file zip reported truncated")
	}
}

// When the file cap leaves candidate files unread the scan must say so —
// this is the honesty bug found live on the jans artifact (650 XML files,
// old cap 200: 527 of 3,096 failing tests reported with no hint the scan
// was partial).
func TestScanZipTruncatedByFileCap(t *testing.T) {
	oldFiles := maxJUnitZipFiles
	maxJUnitZipFiles = 3
	defer func() { maxJUnitZipFiles = oldFiles }()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < 5; i++ {
		w, _ := zw.Create(fmt.Sprintf("TEST-suite%d.xml", i))
		fmt.Fprintf(w, `<testsuite name="s%d" tests="1"><testcase classname="C%d" name="t"><failure/></testcase></testsuite>`, i, i)
	}
	zw.Close()
	fails, _, files, truncated := scanJUnitZip(buf.Bytes())
	if !truncated {
		t.Error("file cap exhausted with candidates left, not reported truncated")
	}
	if files != 3 || len(fails) != 3 {
		t.Errorf("files=%d fails=%d, want 3/3", files, len(fails))
	}
}

// Small files are read first so the byte budget covers many small reports
// before one huge file — and a budget miss is reported as truncation.
func TestScanZipSmallFirstAndByteBudget(t *testing.T) {
	oldBudget := maxJUnitReadBytes
	maxJUnitReadBytes = 4096
	defer func() { maxJUnitReadBytes = oldBudget }()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Big file FIRST in zip order: padded past the budget.
	w, _ := zw.Create("a-huge-results.xml")
	fmt.Fprintf(w, `<testsuite name="big" tests="1"><testcase classname="Big" name="t"><failure/></testcase><!-- %s --></testsuite>`,
		strings.Repeat("x", 8000))
	w, _ = zw.Create("z-small.xml")
	w.Write([]byte(`<testsuite name="s" tests="1"><testcase classname="Small" name="t"><failure/></testcase></testsuite>`))
	zw.Close()
	fails, _, files, truncated := scanJUnitZip(buf.Bytes())
	if files != 1 || len(fails) != 1 || fails[0] != "Small.t" {
		t.Errorf("files=%d fails=%v, want the small report parsed first", files, fails)
	}
	if !truncated {
		t.Error("byte budget left the big candidate unread, not reported truncated")
	}
}
