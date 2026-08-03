package api

// JUnit XML test-report parsing — the fallback exact-name source for --run
// deep dives. When a failed job's log speaks no recognized framework format
// (see docs/flaky-frameworks.md), the run's uploaded artifacts may still
// carry JUnit XML test reports (pytest --junitxml, Maven surefire, Gradle,
// jest-junit, ctest --output-junit, and most CI reporters emit it). Those
// record exact failing-test names regardless of what the console output
// looked like.
//
// Honesty notes:
//   - Artifacts are RUN-scoped, not job-scoped: a name from a report can't
//     be attributed to a specific failed job, so artifact-derived tests are
//     reported at run level, labeled with the artifact they came from.
//   - A report that records zero failures is a statement about the tests it
//     covers, not proof the job failure wasn't a test — the failing shard
//     may simply not have uploaded its report.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

// Per-file and per-artifact parse budgets. JUnit files are usually small;
// a multi-megabyte XML is almost always console output stuffed into an
// attachment, and an unbounded read would let one artifact eat the run.
const (
	maxJUnitXMLBytes  = 10 << 20 // per XML file (uncompressed)
	maxJUnitZipFiles  = 200      // XML files inspected per artifact
	maxJUnitReadBytes = 64 << 20 // total uncompressed bytes per artifact
)

// junitCase is one <testcase>. Only direct <failure>/<error> children count
// as failures: surefire's <flakyFailure>/<rerunFailure> mark retries that
// ultimately PASSED, and <skipped> is not a failure.
type junitCase struct {
	Name      string     `xml:"name,attr"`
	Classname string     `xml:"classname,attr"`
	Failures  []struct{} `xml:"failure"`
	Errors    []struct{} `xml:"error"`
}

// junitSuite is a <testsuite>, possibly nesting further suites.
type junitSuite struct {
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

// parseJUnitXML extracts failing-test names from one XML document. ok is
// false when the document is not JUnit-shaped (wrong root element or
// unparseable), so callers can tell "not a test report" from "a report
// with no failures". cases counts all testcases seen, failures only the
// failed ones.
func parseJUnitXML(data []byte) (failures []string, cases int, ok bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	// Find the root element without decoding the whole document first.
	var root xml.StartElement
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, 0, false
		}
		if se, isStart := tok.(xml.StartElement); isStart {
			root = se
			break
		}
	}
	var suites []junitSuite
	switch root.Name.Local {
	case "testsuites":
		var doc struct {
			Suites []junitSuite `xml:"testsuite"`
		}
		if err := dec.DecodeElement(&doc, &root); err != nil {
			return nil, 0, false
		}
		suites = doc.Suites
	case "testsuite":
		var s junitSuite
		if err := dec.DecodeElement(&s, &root); err != nil {
			return nil, 0, false
		}
		suites = []junitSuite{s}
	default:
		return nil, 0, false
	}
	var walk func(s junitSuite)
	walk = func(s junitSuite) {
		for _, c := range s.Cases {
			cases++
			if len(c.Failures) > 0 || len(c.Errors) > 0 {
				if name := junitCaseName(c); name != "" {
					failures = append(failures, name)
				}
			}
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	for _, s := range suites {
		walk(s)
	}
	return failures, cases, true
}

// junitCaseName joins classname and name the way most tooling displays
// them. Some emitters repeat the classname inside name; don't double it.
func junitCaseName(c junitCase) string {
	name := strings.TrimSpace(c.Name)
	class := strings.TrimSpace(c.Classname)
	if name == "" {
		return class
	}
	if class == "" || strings.HasPrefix(name, class) {
		if len(name) > 200 {
			return ""
		}
		return name
	}
	full := class + "." + name
	if len(full) > 200 {
		return ""
	}
	return full
}

// scanJUnitZip walks an artifact zip and collects failing-test names from
// every test report inside — JUnit-shaped XML, TRX (see trx.go) or NUnit3
// (see nunit.go) — deduped in encounter order. xmlFiles counts the files
// that parsed as reports of any format.
func scanJUnitZip(zipData []byte) (failures []string, cases, xmlFiles int) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, 0, 0
	}
	seen := map[string]bool{}
	inspected := 0
	var budget int64 = maxJUnitReadBytes
	for _, f := range zr.File {
		lower := strings.ToLower(f.Name)
		isTRX := strings.HasSuffix(lower, ".trx")
		if !strings.HasSuffix(lower, ".xml") && !isTRX {
			continue
		}
		if inspected == maxJUnitZipFiles || budget <= 0 {
			break
		}
		inspected++
		if f.UncompressedSize64 > maxJUnitXMLBytes {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		// +1 over the declared size: a lying zip header must not stream
		// unbounded bytes through us.
		data, err := io.ReadAll(io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		if err != nil {
			continue
		}
		budget -= int64(len(data))
		var names []string
		var n int
		var isReport bool
		if isTRX {
			names, n, isReport = parseTRX(data)
		} else if names, n, isReport = parseJUnitXML(data); !isReport {
			// A .xml file can hold a TRX document too (custom log names).
			if names, n, isReport = parseTRX(data); !isReport {
				// Or an NUnit3 test-run (nunit3-console, Unity test runner).
				names, n, isReport = parseNUnit3(data)
			}
		}
		if !isReport {
			continue
		}
		xmlFiles++
		cases += n
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				failures = append(failures, name)
			}
		}
	}
	return failures, cases, xmlFiles
}
