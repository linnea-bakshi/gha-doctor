package api

// NUnit3 test-result parsing — the third report format in the artifact
// fallback, alongside JUnit XML and TRX. `nunit3-console --result`,
// NunitXml.TestLogger (`dotnet test --logger nunit`) and — the big
// population — Unity's test runner (game-ci/unity-test-runner uploads
// *-results.xml artifacts by default) all write it. Anchored on a real
// failing EditMode report uploaded by unitystation/unitystation run
// 30821876456 (Unity engine-version 3.5.0.0) and real NunitXml.TestLogger
// output generated on a runner (gd-private-testbed nunit-report.yml).
//
// Shape: root <test-run> (no namespace, unlike TRX) with a testcasecount
// attribute; suites nest via <test-suite>, actual tests are <test-case>
// leaves (a test-case never contains another). Suites carry result="Failed"
// too — only test-case elements are counted, so an assembly's aggregate
// failure can't double-count its cases.
//
// Failing outcomes: result="Failed" — except label="Cancelled", which
// means the run was aborted around the test, not that the test failed
// (the same bar that keeps TRX's Aborted out). label="Error" (exception)
// and label="Invalid" (non-runnable test) stay in: both fail CI and name
// a real culprit. Cascade failures (site="Parent"/"Child" when a fixture's
// OneTimeSetUp fails) are counted like any other failure — the same
// honest-but-capped treatment console extractors give framework cascades.

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// parseNUnit3 extracts failing-test names from one NUnit3 test-result
// document. ok is false when the document is not NUnit3-shaped (wrong root
// element, namespaced root, or missing the testcasecount fingerprint), so
// callers can tell "not a test report" from "a report with no failures".
// cases counts test-case leaves, failures only the failing ones.
func parseNUnit3(data []byte) (failures []string, cases int, ok bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err != nil {
			// EOF before a root element = not a report; after a valid
			// root, a truncated tail keeps what was already parsed.
			return failures, cases, sawRoot
		}
		se, isStart := tok.(xml.StartElement)
		if !isStart {
			continue
		}
		if !sawRoot {
			if se.Name.Local != "test-run" || se.Name.Space != "" {
				return nil, 0, false
			}
			hasCount := false
			for _, a := range se.Attr {
				if a.Name.Local == "testcasecount" {
					hasCount = true
					break
				}
			}
			if !hasCount {
				return nil, 0, false
			}
			sawRoot = true
			continue
		}
		if se.Name.Local != "test-case" {
			continue
		}
		var name, fullname, result, label string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "name":
				name = a.Value
			case "fullname":
				fullname = a.Value
			case "result":
				result = a.Value
			case "label":
				label = a.Value
			}
		}
		cases++
		if result == "Failed" && !strings.EqualFold(label, "Cancelled") {
			if fullname != "" {
				name = fullname
			}
			if name != "" {
				failures = append(failures, name)
			}
		}
		// Attributes are all we need; skip children (failure messages,
		// output, properties) so their elements can't be misread.
		if err := dec.Skip(); err != nil {
			return failures, cases, true
		}
	}
}
