package api

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestParseJUnitXML(t *testing.T) {
	cases := []struct {
		name      string
		xml       string
		wantOK    bool
		wantFails []string
		wantCases int
	}{
		{
			name: "testsuites root with failure and error",
			xml: `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="pkg" tests="3">
    <testcase classname="tests.test_auth" name="test_login_expired">
      <failure message="assert 401 == 200">trace</failure>
    </testcase>
    <testcase classname="tests.test_auth" name="test_ok"/>
    <testcase classname="tests.test_db" name="test_conn">
      <error message="ConnectionRefused"/>
    </testcase>
  </testsuite>
</testsuites>`,
			wantOK:    true,
			wantFails: []string{"tests.test_auth.test_login_expired", "tests.test_db.test_conn"},
			wantCases: 3,
		},
		{
			name: "testsuite root, nested suites",
			xml: `<testsuite name="outer">
  <testsuite name="inner">
    <testcase classname="A" name="t1"><failure/></testcase>
  </testsuite>
  <testcase classname="B" name="t2"/>
</testsuite>`,
			wantOK:    true,
			wantFails: []string{"A.t1"},
			wantCases: 2,
		},
		{
			name: "skipped and flakyFailure are not failures",
			xml: `<testsuite>
  <testcase classname="C" name="skipped_one"><skipped/></testcase>
  <testcase classname="C" name="flaky_but_passed"><flakyFailure message="1st try"/></testcase>
  <testcase classname="C" name="rerun_passed"><rerunFailure message="1st try"/></testcase>
</testsuite>`,
			wantOK:    true,
			wantFails: nil,
			wantCases: 3,
		},
		{
			name:      "empty classname keeps bare name",
			xml:       `<testsuite><testcase name="standalone_test"><failure/></testcase></testsuite>`,
			wantOK:    true,
			wantFails: []string{"standalone_test"},
			wantCases: 1,
		},
		{
			name:      "name already prefixed with classname is not doubled",
			xml:       `<testsuite><testcase classname="pkg.Class" name="pkg.Class.test_x"><failure/></testcase></testsuite>`,
			wantOK:    true,
			wantFails: []string{"pkg.Class.test_x"},
			wantCases: 1,
		},
		{
			name:   "non-junit root is not a report",
			xml:    `<coverage line-rate="0.9"><packages/></coverage>`,
			wantOK: false,
		},
		{
			name:   "malformed xml is not a report",
			xml:    `<testsuite><testcase name="x"`,
			wantOK: false,
		},
		{
			name:   "unsupported charset is skipped honestly",
			xml:    `<?xml version="1.0" encoding="ISO-8859-1"?><testsuite><testcase name="x"><failure/></testcase></testsuite>`,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fails, n, ok := parseJUnitXML([]byte(tc.xml))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if n != tc.wantCases {
				t.Errorf("cases = %d, want %d", n, tc.wantCases)
			}
			if len(fails) != len(tc.wantFails) {
				t.Fatalf("failures = %v, want %v", fails, tc.wantFails)
			}
			for i := range fails {
				if fails[i] != tc.wantFails[i] {
					t.Errorf("failure[%d] = %q, want %q", i, fails[i], tc.wantFails[i])
				}
			}
		})
	}
}

// buildZip makes an in-memory zip from name → content pairs.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestScanJUnitZip(t *testing.T) {
	data := buildZip(t, map[string]string{
		"results/junit-shard1.xml": `<testsuite><testcase classname="A" name="t1"><failure/></testcase></testsuite>`,
		"results/junit-shard2.xml": `<testsuite>
  <testcase classname="A" name="t1"><failure/></testcase>
  <testcase classname="B" name="t2"><error/></testcase>
</testsuite>`,
		"coverage.xml": `<coverage line-rate="0.5"/>`,
		"notes.txt":    "not xml",
	})
	fails, cases, xmlFiles := scanJUnitZip(data)
	if xmlFiles != 2 {
		t.Errorf("junit files = %d, want 2 (coverage.xml is not junit-shaped)", xmlFiles)
	}
	if cases != 3 {
		t.Errorf("cases = %d, want 3", cases)
	}
	// A.t1 appears in both shards — deduped across files.
	want := map[string]bool{"A.t1": true, "B.t2": true}
	if len(fails) != len(want) {
		t.Fatalf("failures = %v, want keys of %v", fails, want)
	}
	for _, f := range fails {
		if !want[f] {
			t.Errorf("unexpected failure %q", f)
		}
	}
}

func TestScanJUnitZipNotAZip(t *testing.T) {
	fails, cases, files := scanJUnitZip([]byte("definitely not a zip"))
	if fails != nil || cases != 0 || files != 0 {
		t.Errorf("garbage input should yield nothing, got %v/%d/%d", fails, cases, files)
	}
}

func TestJunitArtifactScore(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"junit-results-ubuntu", 3},
		{"surefire-reports", 3},
		{"test-results (3.12, ubuntu-latest)", 2},
		{"Test Reports", 2},
		{"tests", 1},
		{"coverage-report", 0},
		{"playwright-report", 0},
		{"test-screenshots", 0},
		{"binaries", 0},
	}
	for _, tc := range cases {
		if got := junitArtifactScore(tc.name); got != tc.want {
			t.Errorf("score(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestJunitCaseNameCaps(t *testing.T) {
	long := strings.Repeat("x", 300)
	if got := junitCaseName(junitCase{Name: long}); got != "" {
		t.Errorf("over-long name should be dropped, got %q", got)
	}
}
