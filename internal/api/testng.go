package api

// TestNG native test-report parsing (testng-results.xml) — the fourth
// format in the artifact fallback (see junit.go). Maven surefire runs
// TestNG suites emit BOTH JUnit-format TEST-*.xml and testng-results.xml,
// but plain `testng` invocations, Gradle TestNG tasks and selenium-style
// automation frameworks often upload only the native file.
//
// Shape (anchored on a real 35MB testng-results.xml from a failed
// JanssenProject/jans integration run, artifact "test-reports-PGSQL"):
//
//	<testng-results retried="4713" ignored="4713" total="11277" …>
//	  <suite name="jansAuthClient" …>
//	    <test name="JsonApplier Client test" …>
//	      <class name="io.jans.as.client.json.JsonApplierTest">
//	        <test-method name="apply…" status="PASS" signature="…"/>
//	        <test-method name="setUp" is-config="true" status="PASS"/>
//	        <test-method name="requestX" status="SKIP" retried="true"/>
//	        <test-method name="requestX" status="FAIL"/>
//
// Honesty rules, all observed live in that file:
//   - status="FAIL" is a final failure — count it. TestNG RetryAnalyzer
//     re-runs are recorded as status="SKIP" retried="true" and must not
//     count as failures OR as executed cases (they are attempts, not
//     tests; the file above carries 4,713 of them).
//   - is-config="true" methods (@BeforeClass and friends) are lifecycle
//     hooks, not tests: excluded from the case count. A FAILing config
//     method is still named — it is a real failure with a real name.
//   - Data-provider re-invocations share the method name; the zip-level
//     dedupe collapses them, matching how the console extractors
//     aggregate parameterized runs.
//   - testng-failed.xml (the rerun SUITE definition TestNG writes next to
//     the results) has root <suite> and is rejected — it lists tests to
//     re-run, not results.

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// parseTestNG extracts failing-test names from a testng-results.xml
// document. ok is false when the document is not TestNG-shaped. cases
// counts executed test methods (config methods and retry attempts
// excluded); failures holds class-qualified names in document order,
// duplicates preserved (callers dedupe).
func parseTestNG(data []byte) (failures []string, cases int, ok bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	rootSeen := false
	var class string // innermost enclosing <class name="…">
	var classDepth int
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				if t.Name.Local != "testng-results" {
					return nil, 0, false
				}
				rootSeen = true
				continue
			}
			switch t.Name.Local {
			case "class":
				class = attrValue(t, "name")
				classDepth = depth
			case "test-method":
				if attrValue(t, "is-config") == "true" {
					continue
				}
				if attrValue(t, "retried") == "true" {
					continue
				}
				cases++
				if attrValue(t, "status") == "FAIL" {
					if name := testNGName(class, attrValue(t, "name")); name != "" {
						failures = append(failures, name)
					}
				}
			}
		case xml.EndElement:
			if depth == classDepth && t.Name.Local == "class" {
				class = ""
				classDepth = 0
			}
			depth--
		}
	}
	return failures, cases, rootSeen
}

// testNGName joins class and method the way TestNG's own reports display
// them, with the same length guard as junitCaseName.
func testNGName(class, method string) string {
	method = strings.TrimSpace(method)
	class = strings.TrimSpace(class)
	if method == "" {
		return class
	}
	full := method
	if class != "" && !strings.HasPrefix(method, class) {
		full = class + "." + method
	}
	if len(full) > 200 {
		return ""
	}
	return full
}

// attrValue returns the value of a start element's attribute, or "".
func attrValue(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
