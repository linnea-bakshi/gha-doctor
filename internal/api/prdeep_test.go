package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

var prT0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// prServer serves a 3-commit PR whose head commit has three runs: a green
// lint run, a failed CI run (attempt 2 — someone hit re-run), and one still
// in progress. The failed run's jobs/baseline endpoints are stubbed so the
// nested AnalyzeRun succeeds.
func prServer(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/123", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"number": 123, "title": "Add feature", "state": "open", "merged": false,
			"draft": true, "html_url": "https://github.example/o/r/pull/123",
			"created_at": prT0.Add(-24 * time.Hour), "commits": 3,
			"user": map[string]any{"login": "alice"},
			"head": map[string]any{"sha": "abcdef0123456789", "ref": "feat"},
			"base": map[string]any{"ref": "main"},
		})
	})
	mux.HandleFunc("/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("head_sha"); got != "abcdef0123456789" {
			t.Errorf("head_sha param = %q, want the PR head SHA", got)
		}
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{
			{ID: 11, Name: "Lint", WorkflowID: 5, RunNumber: 7, Event: "pull_request",
				Status: "completed", Conclusion: "success", RunAttempt: 1,
				CreatedAt: prT0, RunStartedAt: prT0.Add(10 * time.Second), UpdatedAt: prT0.Add(70 * time.Second)},
			{ID: 12, Name: "CI", WorkflowID: 6, RunNumber: 41, Event: "pull_request",
				Status: "completed", Conclusion: "failure", RunAttempt: 2,
				CreatedAt: prT0, RunStartedAt: prT0.Add(5 * time.Second), UpdatedAt: prT0.Add(305 * time.Second)},
			{ID: 13, Name: "E2E", WorkflowID: 8, RunNumber: 3, Event: "pull_request",
				Status: "in_progress", RunAttempt: 1,
				CreatedAt: prT0.Add(2 * time.Second), RunStartedAt: prT0.Add(12 * time.Second)},
		}})
	})
	// Nested deep dive of failed run 12.
	mux.HandleFunc("/repos/o/r/actions/runs/12/jobs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{{
			ID: 120, Name: "test", RunAttempt: 2, Status: "completed", Conclusion: "failure",
			CreatedAt: prT0.Add(5 * time.Second), StartedAt: prT0.Add(15 * time.Second),
			CompletedAt: prT0.Add(300 * time.Second),
		}}})
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/6/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{}})
	})
	c, srv := testClient(mux)
	t.Cleanup(srv.Close)
	return c
}

func TestAnalyzePR(t *testing.T) {
	c := prServer(t)
	d, err := c.AnalyzePR("o", "r", 123, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "open" || !d.Draft || d.Author != "alice" || d.Commits != 3 {
		t.Errorf("PR meta wrong: %+v", d)
	}
	if d.Branch != "feat" || d.BaseBranch != "main" {
		t.Errorf("branches = %q -> %q, want feat -> main", d.Branch, d.BaseBranch)
	}
	if len(d.Runs) != 3 {
		t.Fatalf("Runs = %d, want 3", len(d.Runs))
	}
	// Sorted by start time: CI (t+5) before Lint (t+10) before E2E (t+12).
	if d.Runs[0].Workflow != "CI" || d.Runs[1].Workflow != "Lint" || d.Runs[2].Workflow != "E2E" {
		t.Errorf("run order = %s/%s/%s, want CI/Lint/E2E", d.Runs[0].Workflow, d.Runs[1].Workflow, d.Runs[2].Workflow)
	}
	if d.Runs[0].WallSec != 300 {
		t.Errorf("CI WallSec = %v, want 300", d.Runs[0].WallSec)
	}
	if d.Runs[2].WallSec != 0 {
		t.Errorf("in-progress run must have no wall clock, got %v", d.Runs[2].WallSec)
	}
	if d.RetriedRuns != 1 {
		t.Errorf("RetriedRuns = %d, want 1 (CI is attempt 2)", d.RetriedRuns)
	}
	// First red: CI completed at t+305; earliest created_at is t+0.
	if d.FeedbackSec != 305 {
		t.Errorf("FeedbackSec = %v, want 305", d.FeedbackSec)
	}
	if !strings.Contains(d.FeedbackNote, "first red") {
		t.Errorf("FeedbackNote = %q, want a first-red note", d.FeedbackNote)
	}
	if !strings.Contains(d.RunsNote, "3 commits") {
		t.Errorf("RunsNote = %q, want the multi-commit caveat", d.RunsNote)
	}
	if d.Deep == nil {
		t.Fatal("Deep = nil, want a dive into failed run 12")
	}
	if d.Deep.RunID != 12 || d.Deep.Conclusion != "failure" {
		t.Errorf("Deep run = %d (%s), want 12 (failure)", d.Deep.RunID, d.Deep.Conclusion)
	}
	if !strings.Contains(d.DeepNote, "latest failed run") {
		t.Errorf("DeepNote = %q, want why-this-run note", d.DeepNote)
	}
}

// A PR whose head commit is all green gets no nested dive, just a pointer.
func TestAnalyzePRAllGreen(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "title": "Green", "state": "closed", "merged": true, "commits": 1,
			"created_at": prT0, "user": map[string]any{"login": "bob"},
			"head": map[string]any{"sha": "feedbead", "ref": "fix"},
			"base": map[string]any{"ref": "main"},
		})
	})
	mux.HandleFunc("/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{
			{ID: 21, Name: "CI", Status: "completed", Conclusion: "success", RunAttempt: 1,
				CreatedAt: prT0, RunStartedAt: prT0.Add(3 * time.Second), UpdatedAt: prT0.Add(63 * time.Second)},
		}})
	})
	c, srv := testClient(mux)
	defer srv.Close()
	d, err := c.AnalyzePR("o", "r", 7, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "merged" {
		t.Errorf("State = %q, want merged (merged flag wins over closed)", d.State)
	}
	if d.Deep != nil {
		t.Errorf("Deep = %+v, want nil when nothing failed", d.Deep)
	}
	if !strings.Contains(d.DeepNote, "no failed runs") {
		t.Errorf("DeepNote = %q, want the all-green pointer", d.DeepNote)
	}
	if d.FeedbackSec != 63 || !strings.Contains(d.FeedbackNote, "all-green") {
		t.Errorf("feedback = %v %q, want 63s all-green", d.FeedbackSec, d.FeedbackNote)
	}
	if d.RunsNote != "" {
		t.Errorf("RunsNote = %q, want empty for a 1-commit PR with runs", d.RunsNote)
	}
}

// Zero runs on the head commit is stated, not silently empty.
func TestAnalyzePRNoRuns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/9", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"number": 9, "title": "Docs", "state": "open", "commits": 1,
			"created_at": prT0, "user": map[string]any{"login": "carol"},
			"head": map[string]any{"sha": "0123abcd", "ref": "docs"},
			"base": map[string]any{"ref": "main"},
		})
	})
	mux.HandleFunc("/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []Run{}})
	})
	c, srv := testClient(mux)
	defer srv.Close()
	d, err := c.AnalyzePR("o", "r", 9, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Runs) != 0 || !strings.Contains(d.RunsNote, "no workflow runs") {
		t.Errorf("want a no-runs note, got runs=%d note=%q", len(d.Runs), d.RunsNote)
	}
	if d.Deep != nil || d.DeepNote != "" {
		t.Errorf("no runs must mean no dive and no pointer, got %+v %q", d.Deep, d.DeepNote)
	}
}

func TestParsePRRef(t *testing.T) {
	cases := []struct {
		in          string
		num         int
		owner, repo string
		wantErr     bool
	}{
		{"123", 123, "", "", false},
		{"#123", 123, "", "", false},
		{" 42 ", 42, "", "", false},
		{"https://github.com/foo/bar/pull/77", 77, "foo", "bar", false},
		{"https://github.com/foo/bar/pull/77/files", 77, "foo", "bar", false},
		{"http://ghe.corp/foo/bar/pull/8", 8, "foo", "bar", false},
		{"0", 0, "", "", true},
		{"-3", 0, "", "", true},
		{"latest", 0, "", "", true},
		{"", 0, "", "", true},
	}
	for _, c := range cases {
		n, o, r, err := ParsePRRef(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParsePRRef(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if n != c.num || o != c.owner || r != c.repo {
			t.Errorf("ParsePRRef(%q) = %d %q %q, want %d %q %q", c.in, n, o, r, c.num, c.owner, c.repo)
		}
	}
}
