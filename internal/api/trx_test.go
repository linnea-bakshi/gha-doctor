package api

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

// Real TRX written by `dotnet test --logger trx` (xunit adapter) on a
// GitHub runner: one passing, one failing test. testName is already fully
// qualified, so the class prefix must not be doubled.
func TestParseTRXRealFailing(t *testing.T) {
	data, err := os.ReadFile("testdata/trx/xunit-failing.trx")
	if err != nil {
		t.Fatal(err)
	}
	fails, cases, ok := parseTRX(data)
	if !ok {
		t.Fatal("real TRX not recognized")
	}
	if cases != 2 {
		t.Fatalf("cases = %d, want 2", cases)
	}
	want := []string{"Payments.Tests.PaymentsTrxSuite.Refund_Partial_ReturnsOk"}
	if len(fails) != 1 || fails[0] != want[0] {
		t.Fatalf("failures = %q, want %q", fails, want)
	}
}

// Real TRX from a public nunit/nunit run artifact: all tests passed.
// ok must still be true so the caller can report "reports record N cases
// and no failures" instead of "no test reports found".
func TestParseTRXRealPassing(t *testing.T) {
	data, err := os.ReadFile("testdata/trx/nunit-passing.trx")
	if err != nil {
		t.Fatal(err)
	}
	fails, cases, ok := parseTRX(data)
	if !ok {
		t.Fatal("real TRX not recognized")
	}
	if cases != 2 || len(fails) != 0 {
		t.Fatalf("cases=%d fails=%q, want 2 and none", cases, fails)
	}
}

func TestParseTRXSynthetic(t *testing.T) {
	const ns = `xmlns="http://microsoft.com/schemas/VisualStudio/TeamTest/2010"`
	cases := []struct {
		name      string
		doc       string
		wantOK    bool
		wantFails []string
		wantCases int
	}{
		{
			name: "className qualifies bare testName; assembly suffix stripped",
			doc: `<TestRun ` + ns + `>
  <Results>
    <UnitTestResult testId="a" testName="Refund_Fails" outcome="Failed"/>
    <UnitTestResult testId="b" testName="Charge_OK" outcome="Passed"/>
  </Results>
  <TestDefinitions>
    <UnitTest id="a"><TestMethod className="Pay.Suite, Pay.Tests, Version=1.0.0.0" name="Refund_Fails"/></UnitTest>
    <UnitTest id="b"><TestMethod className="Pay.Suite" name="Charge_OK"/></UnitTest>
  </TestDefinitions>
</TestRun>`,
			wantOK:    true,
			wantFails: []string{"Pay.Suite.Refund_Fails"},
			wantCases: 2,
		},
		{
			name: "Timeout and Error count; NotExecuted, Inconclusive, Aborted don't",
			doc: `<TestRun ` + ns + `>
  <Results>
    <UnitTestResult testName="T.slow" outcome="Timeout"/>
    <UnitTestResult testName="T.err" outcome="Error"/>
    <UnitTestResult testName="T.skip" outcome="NotExecuted"/>
    <UnitTestResult testName="T.meh" outcome="Inconclusive"/>
    <UnitTestResult testName="T.stop" outcome="Aborted"/>
  </Results>
</TestRun>`,
			wantOK:    true,
			wantFails: []string{"T.slow", "T.err"},
			wantCases: 5,
		},
		{
			name: "data-driven inner rows are the leaves; parent aggregate not double-counted",
			doc: `<TestRun ` + ns + `>
  <Results>
    <UnitTestResult testId="p" testName="Rows" outcome="Failed">
      <InnerResults>
        <UnitTestResult testName="Rows(1)" outcome="Passed"/>
        <UnitTestResult testName="Rows(2)" outcome="Failed"/>
      </InnerResults>
    </UnitTestResult>
  </Results>
  <TestDefinitions>
    <UnitTest id="p"><TestMethod className="Data.Suite" name="Rows"/></UnitTest>
  </TestDefinitions>
</TestRun>`,
			wantOK:    true,
			wantFails: []string{"Data.Suite.Rows(2)"},
			wantCases: 2,
		},
		{
			name:   "wrong namespace rejected",
			doc:    `<TestRun xmlns="http://example.com/not-teamtest"><Results/></TestRun>`,
			wantOK: false,
		},
		{
			name:   "wrong root rejected",
			doc:    `<testsuites ` + ns + `/>`,
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
			fails, n, ok := parseTRX([]byte(tc.doc))
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

// A .trx inside an artifact zip is scanned; so is a TRX document that a
// custom logger saved with a .xml extension.
func TestScanZipFindsTRX(t *testing.T) {
	trx, err := os.ReadFile("testdata/trx/xunit-failing.trx")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"results.trx", "results.xml"} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(entry)
		w.Write(trx)
		// A JUnit file alongside must still parse independently.
		w2, _ := zw.Create("junit.xml")
		w2.Write([]byte(`<testsuite name="s"><testcase classname="C" name="t_ok"/></testsuite>`))
		zw.Close()
		fails, cases, files := scanJUnitZip(buf.Bytes())
		if files != 2 {
			t.Fatalf("[%s] report files = %d, want 2", entry, files)
		}
		if cases != 3 {
			t.Fatalf("[%s] cases = %d, want 3", entry, cases)
		}
		if len(fails) != 1 || fails[0] != "Payments.Tests.PaymentsTrxSuite.Refund_Partial_ReturnsOk" {
			t.Fatalf("[%s] failures = %q", entry, fails)
		}
	}
}

func TestArtifactScoreTRX(t *testing.T) {
	if s := junitArtifactScore("trx-results"); s != 3 {
		t.Fatalf("trx-results score = %d, want 3", s)
	}
}
