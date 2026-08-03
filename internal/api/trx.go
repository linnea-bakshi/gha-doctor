package api

// TRX (Visual Studio TestRun) test-report parsing — the .NET counterpart to
// JUnit XML in the artifact fallback. `dotnet test --logger trx` (and VSTest
// on any runner) writes these; .NET repos routinely upload them as run
// artifacts while the console output is wrapped by build tooling (Cake,
// MSBuild, tee'd logs) that our console extractors can't always read.
//
// Only the 2010 TeamTest schema exists in practice (stable since VS2010);
// the root element must be <TestRun> in a TeamTest namespace so arbitrary
// XML can't masquerade as a test report.
//
// Failing outcomes are Failed, Error and Timeout — the same bar as JUnit's
// direct <failure>/<error> children. NotExecuted/Inconclusive/Warning are
// not failures, and Aborted usually means "cancelled", not "test failed".
// Data-driven tests nest per-row results under <InnerResults>; only leaf
// results are counted so an aggregate parent can't double-count its rows.

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// trxResult is one <UnitTestResult>, possibly nesting per-data-row results.
type trxResult struct {
	TestID   string      `xml:"testId,attr"`
	TestName string      `xml:"testName,attr"`
	Outcome  string      `xml:"outcome,attr"`
	Inner    []trxResult `xml:"InnerResults>UnitTestResult"`
}

// parseTRX extracts failing-test names from one TRX document. ok is false
// when the document is not TRX-shaped (wrong root element or namespace, or
// unparseable), so callers can tell "not a test report" from "a report with
// no failures". cases counts leaf results, failures only the failing ones.
func parseTRX(data []byte) (failures []string, cases int, ok bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
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
	if root.Name.Local != "TestRun" ||
		!strings.Contains(root.Name.Space, "microsoft.com/schemas/VisualStudio/TeamTest") {
		return nil, 0, false
	}
	var doc struct {
		Results []trxResult `xml:"Results>UnitTestResult"`
		Defs    []struct {
			ID     string `xml:"id,attr"`
			Method struct {
				ClassName string `xml:"className,attr"`
				Name      string `xml:"name,attr"`
			} `xml:"TestMethod"`
		} `xml:"TestDefinitions>UnitTest"`
	}
	if err := dec.DecodeElement(&doc, &root); err != nil {
		return nil, 0, false
	}
	class := make(map[string]string, len(doc.Defs))
	for _, d := range doc.Defs {
		if d.ID != "" && d.Method.ClassName != "" {
			class[d.ID] = trxClassName(d.Method.ClassName)
		}
	}
	var walk func(r trxResult)
	walk = func(r trxResult) {
		if len(r.Inner) > 0 {
			for _, in := range r.Inner {
				// Inner rows may omit testId; inherit the parent's for the
				// class lookup.
				if in.TestID == "" {
					in.TestID = r.TestID
				}
				walk(in)
			}
			return
		}
		cases++
		switch r.Outcome {
		case "Failed", "Error", "Timeout":
			name := junitCaseName(junitCase{Name: r.TestName, Classname: class[r.TestID]})
			if name != "" {
				failures = append(failures, name)
			}
		}
	}
	for _, r := range doc.Results {
		walk(r)
	}
	return failures, cases, true
}

// trxClassName strips the assembly qualification MSTest appends to
// className ("Ns.Class, Assembly, Version=…" → "Ns.Class").
func trxClassName(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
