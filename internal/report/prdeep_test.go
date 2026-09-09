package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

func prFixture() *api.PRDeep {
	return &api.PRDeep{
		Repo: "o/r", Number: 123, Title: "Add feature", Author: "alice",
		State: "open", Draft: true, URL: "https://github.example/o/r/pull/123",
		Branch: "feat", BaseBranch: "main", Commits: 3,
		CreatedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		HeadSHA:   "abcdef0123456789",
		Runs: []api.PRRun{
			{RunID: 12, Workflow: "CI", Event: "pull_request", Status: "completed",
				Conclusion: "failure", Attempt: 2, WallSec: 300},
			{RunID: 11, Workflow: "Lint", Event: "pull_request", Status: "completed",
				Conclusion: "success", Attempt: 1, WallSec: 60},
			{RunID: 13, Workflow: "E2E", Event: "pull_request", Status: "in_progress", Attempt: 1},
		},
		RunsNote:     "PR has 3 commits; only the head commit's runs are analyzed",
		RetriedRuns:  1,
		FeedbackSec:  305,
		FeedbackNote: "time to first red on the head commit",
		DeepNote:     "diving into the latest failed run on the head commit (CI #41)",
		Deep:         deepFixture(),
	}
}

func TestPRDeepTerminal(t *testing.T) {
	var b strings.Builder
	PRDeep(&b, Style{Plain: true}, prFixture())
	out := b.String()
	for _, want := range []string{
		"PR #123: Add feature (o/r)",
		"open · draft · feat → main · 3 commits · by alice",
		"Runs on head commit abcdef0 — 3 total, 1 failed, 1 in progress",
		"✗ failure",
		"attempt 2",
		"● in_progress",
		"Feedback: 5m05s (time to first red on the head commit)",
		"attempt >1",
		"only the head commit's runs are analyzed",
		"Run #42: CI (o/r)", // nested RunDeep follows
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q\n%s", want, out)
		}
	}
}

func TestPRDeepTerminalNoRuns(t *testing.T) {
	d := &api.PRDeep{
		Repo: "o/r", Number: 9, Title: "Docs", State: "open", Commits: 1,
		HeadSHA: "0123abcd", RunsNote: "no workflow runs found for head commit 0123abc",
	}
	var b strings.Builder
	PRDeep(&b, Style{Plain: true}, d)
	out := b.String()
	if !strings.Contains(out, "no workflow runs found") {
		t.Errorf("no-runs note missing:\n%s", out)
	}
	if strings.Contains(out, "Runs on head commit") {
		t.Errorf("empty PR must not render a runs header:\n%s", out)
	}
}

func TestPRDeepMarkdown(t *testing.T) {
	var b strings.Builder
	PRDeepMarkdown(&b, prFixture())
	out := b.String()
	for _, want := range []string{
		"## PR #123: Add feature (o/r)",
		"| workflow | result | took | attempt | event |",
		"| CI | failure | 5m00s | 2 | pull_request |",
		"| E2E | in_progress | – | 1 | pull_request |",
		"**1 failed**",
		"**Feedback:** 5m05s",
		"## Run #42: CI (o/r)", // nested dive
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q\n%s", want, out)
		}
	}
}

func TestPRDeepJSONShape(t *testing.T) {
	var b strings.Builder
	if err := PRDeepJSON(&b, prFixture()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(b.String()), &doc); err != nil {
		t.Fatal(err)
	}
	pr, ok := doc["pr"].(map[string]any)
	if !ok {
		t.Fatalf("top-level pr key missing: %v", doc)
	}
	if pr["number"] != float64(123) || pr["head_sha"] != "abcdef0123456789" {
		t.Errorf("pr doc wrong: %v", pr)
	}
	if _, ok := pr["deep"].(map[string]any); !ok {
		t.Errorf("nested deep dive missing: %v", pr)
	}
}
