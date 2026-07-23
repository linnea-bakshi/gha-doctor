package report

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// SARIF 2.1.0 document types (minimal subset).
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	ShortDesc sarifText `json:"shortDescription"`
	HelpURI   string    `json:"helpUri,omitempty"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           sarifRegion   `json:"region"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

const repoURL = "https://github.com/linnea-bakshi/gha-doctor"

// SARIF writes findings as a SARIF 2.1.0 log suitable for
// `github/codeql-action/upload-sarif` (GitHub code scanning).
// baseDir, when non-empty, is stripped so artifact URIs are repo-relative.
func SARIF(w io.Writer, version, baseDir string, findings []lint.Finding) error {
	// Only rules that actually fired, in stable order.
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Rule] = true
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	rules := make([]sarifRule, 0, len(ids))
	for _, id := range ids {
		r := sarifRule{ID: id, HelpURI: repoURL + "/blob/main/docs/rules.md"}
		if m, ok := lint.RuleMeta[id]; ok {
			r.Name = m.Name
			r.ShortDesc = sarifText{Text: m.Short}
			r.HelpURI = fmt.Sprintf("%s/blob/main/docs/rules.md#%s-%s", repoURL, strings.ToLower(m.ID), strings.ToLower(m.Name))
		} else {
			r.ShortDesc = sarifText{Text: id}
		}
		rules = append(rules, r)
	}

	results := make([]sarifResult, 0, len(findings))
	for _, f := range findings {
		level := "note"
		if f.Severity == lint.Warn {
			level = "warning"
		}
		msg := f.Message
		if f.Advice != "" {
			msg += " — fix: " + f.Advice
		}
		uri := f.File
		if baseDir != "" {
			if rel, err := filepath.Rel(baseDir, f.File); err == nil && !strings.HasPrefix(rel, "..") {
				uri = rel
			}
		}
		results = append(results, sarifResult{
			RuleID:  f.Rule,
			Level:   level,
			Message: sarifText{Text: msg},
			Locations: []sarifLocation{{
				PhysicalLocation: sarifPhysical{
					ArtifactLocation: sarifArtifact{URI: filepath.ToSlash(uri)},
					Region:           sarifRegion{StartLine: max(f.Line, 1)},
				},
			}},
		})
	}

	doc := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "gha-doctor",
				Version:        version,
				InformationURI: repoURL,
				Rules:          rules,
			}},
			Results: results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
