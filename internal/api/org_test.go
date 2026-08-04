package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRepoRunStats(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	mkRun := func(daysAgo float64, durMin float64, conclusion string) Run {
		start := now.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
		return Run{
			RunStartedAt: start,
			UpdatedAt:    start.Add(time.Duration(durMin * float64(time.Minute))),
			Conclusion:   conclusion,
		}
	}
	runs := []Run{
		mkRun(1, 10, "success"),
		mkRun(2, 20, "failure"),
		mkRun(3, 30, "success"),
		mkRun(40, 100, "success"), // outside the 30-day window
		{Conclusion: "cancelled"}, // zero timestamps: skipped from durations
	}
	st := repoRunStats("web", runs, 100, now)

	if st.RunsSampled != 5 {
		t.Errorf("RunsSampled = %d, want 5", st.RunsSampled)
	}
	if got, want := st.FailRate, 0.25; !close2(got, want) { // 1 failure / 4 decided
		t.Errorf("FailRate = %.3f, want %.3f", got, want)
	}
	if st.TotalMinutes != 160 {
		t.Errorf("TotalMinutes = %.0f, want 160", st.TotalMinutes)
	}
	// Sample NOT truncated (5 < 100): est is actual last-30d minutes.
	if st.Extrapolated {
		t.Error("Extrapolated = true, want false for untruncated sample")
	}
	if st.Est30dMinutes != 60 {
		t.Errorf("Est30dMinutes = %.0f, want 60 (10+20+30)", st.Est30dMinutes)
	}
	if got := st.LastRun; !got.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("LastRun = %v, want 1 day ago", got)
	}
}

func TestRepoRunStatsExtrapolates(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	// 10 runs of 6 min each spread over 5 days, sample truncated at 10.
	var runs []Run
	for i := 0; i < 10; i++ {
		start := now.Add(-time.Duration(i*12) * time.Hour)
		runs = append(runs, Run{
			RunStartedAt: start,
			UpdatedAt:    start.Add(6 * time.Minute),
			Conclusion:   "success",
		})
	}
	st := repoRunStats("busy", runs, 10, now)
	if !st.Extrapolated {
		t.Fatal("expected extrapolation for truncated sample inside 30d")
	}
	// 60 min over 4.5 days -> 400 min/30d.
	if want := 60.0 / 4.5 * 30; !close2(st.Est30dMinutes, want) {
		t.Errorf("Est30dMinutes = %.1f, want %.1f", st.Est30dMinutes, want)
	}
}

func TestRepoRunStatsBurstIsLowerBoundNotExtrapolated(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	// 10 runs of 6 min each within one hour: extrapolating this rate to
	// 30 days would be absurd (~260k min). Expect a lower bound instead.
	var runs []Run
	for i := 0; i < 10; i++ {
		start := now.Add(-time.Duration(i*6) * time.Minute)
		runs = append(runs, Run{
			RunStartedAt: start,
			UpdatedAt:    start.Add(6 * time.Minute),
			Conclusion:   "success",
		})
	}
	st := repoRunStats("bursty", runs, 10, now)
	if st.Extrapolated {
		t.Error("Extrapolated = true; burst windows must not extrapolate")
	}
	if !st.Truncated {
		t.Error("Truncated = false, want true")
	}
	if st.Est30dMinutes != 60 {
		t.Errorf("Est30dMinutes = %.0f, want observed 60", st.Est30dMinutes)
	}
}

func TestRepoRunStatsSkippedRunsExcludedFromDurations(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	start := now.Add(-24 * time.Hour)
	runs := []Run{
		{RunStartedAt: start, UpdatedAt: start.Add(10 * time.Minute), Conclusion: "success"},
		{RunStartedAt: start, UpdatedAt: start.Add(1 * time.Second), Conclusion: "skipped"},
		{RunStartedAt: start, UpdatedAt: start.Add(1 * time.Second), Conclusion: "skipped"},
		{RunStartedAt: start, UpdatedAt: start.Add(1 * time.Second), Conclusion: "skipped"},
	}
	st := repoRunStats("r", runs, 100, now)
	if st.P50Minutes != 10 {
		t.Errorf("P50Minutes = %.1f, want 10 (skipped runs must not drag p50 down)", st.P50Minutes)
	}
}

func TestAnalyzeOrgEndToEnd(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/orgs/acme/repos":
			fmt.Fprint(w, `[
				{"name":"api","full_name":"acme/api","pushed_at":"2026-07-27T00:00:00Z"},
				{"name":"oldfork","full_name":"acme/oldfork","fork":true},
				{"name":"attic","full_name":"acme/attic","archived":true},
				{"name":"quiet","full_name":"acme/quiet","pushed_at":"2026-07-20T00:00:00Z"}
			]`)
		case strings.HasSuffix(r.URL.Path, "/api/actions/runs"):
			now := time.Now().UTC()
			start := now.Add(-2 * time.Hour).Format(time.RFC3339)
			end := now.Add(-110 * time.Minute).Format(time.RFC3339)
			fmt.Fprintf(w, `{"workflow_runs":[
				{"id":1,"name":"CI","status":"completed","run_started_at":%q,"updated_at":%q,"conclusion":"success"},
				{"id":2,"name":"CI","status":"completed","run_started_at":%q,"updated_at":%q,"conclusion":"failure"}
			]}`, start, end, start, end)
		case strings.HasSuffix(r.URL.Path, "/quiet/actions/runs"):
			fmt.Fprint(w, `{"workflow_runs":[]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oa, err := c.AnalyzeOrg("acme", 20, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if oa.ReposListed != 4 || oa.ReposScanned != 2 {
		t.Errorf("listed/scanned = %d/%d, want 4/2", oa.ReposListed, oa.ReposScanned)
	}
	if oa.SkippedForks != 1 || oa.SkippedArch != 1 {
		t.Errorf("skips = %d forks / %d archived, want 1/1", oa.SkippedForks, oa.SkippedArch)
	}
	if oa.QuietRepos != 1 {
		t.Errorf("QuietRepos = %d, want 1", oa.QuietRepos)
	}
	if len(oa.Repos) != 1 || oa.Repos[0].Repo != "api" {
		t.Fatalf("Repos = %+v, want just acme/api", oa.Repos)
	}
	if got := oa.Repos[0].FailRate; !close2(got, 0.5) {
		t.Errorf("FailRate = %.2f, want 0.50", got)
	}
	if got := oa.TotalFailRate; !close2(got, 0.5) {
		t.Errorf("TotalFailRate = %.2f, want 0.50", got)
	}
}

func TestListOrgReposFallsBackToUser(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/orgs/linus/repos":
			http.Error(w, `{"message":"Not Found"}`, 404)
		case "/users/linus/repos":
			fmt.Fprint(w, `[{"name":"kernel","full_name":"linus/kernel"}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	repos, err := c.ListOrgRepos("linus", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Name != "kernel" {
		t.Errorf("repos = %+v, want [kernel]", repos)
	}
}

func close2(a, b float64) bool {
	d := a - b
	return d < 0.01 && d > -0.01
}

func TestRepoZombieCrons(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	mkSched := func(daysAgo float64, conclusion string) Run {
		created := now.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
		return Run{
			Name: "Nightly", WorkflowID: 7, HTMLURL: "https://example.test/run",
			Event: "schedule", Status: "completed", Conclusion: conclusion,
			CreatedAt: created,
		}
	}
	runs := []Run{
		mkSched(1, "failure"),
		mkSched(2, "failure"),
		mkSched(3, "timed_out"),
		mkSched(4, "failure"),
		mkSched(5, "failure"),
		// push runs never count, however broken
		{Event: "push", Status: "completed", Conclusion: "failure", WorkflowID: 9, CreatedAt: now},
	}
	zs := repoZombieCrons("api", runs)
	if len(zs) != 1 {
		t.Fatalf("repoZombieCrons = %+v, want exactly 1", zs)
	}
	z := zs[0]
	if z.Repo != "api" || z.Workflow != "Nightly" || z.URL != "https://example.test/run" {
		t.Errorf("identity fields wrong: %+v", z)
	}
	if z.Fails != 5 || !z.StreakOpen {
		t.Errorf("Fails=%d StreakOpen=%v, want 5/true (streak reaches sample edge)", z.Fails, z.StreakOpen)
	}
	if z.SpanDays < 3.9 || z.SpanDays > 4.1 {
		t.Errorf("SpanDays = %.2f, want ~4", z.SpanDays)
	}
	if !z.LastFailedAt.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("LastFailedAt = %v, want newest failure", z.LastFailedAt)
	}

	// A success older than the failures closes the streak.
	closed := append([]Run{}, runs...)
	closed = append(closed, mkSched(6, "success"))
	zs = repoZombieCrons("api", closed)
	if len(zs) != 1 || zs[0].StreakOpen {
		t.Errorf("with an older success: %+v, want 1 closed streak", zs)
	}

	// Under the fail gate: nothing reported.
	if zs := repoZombieCrons("api", runs[:3]); len(zs) != 0 {
		t.Errorf("short streak reported: %+v", zs)
	}
}

func TestFinishOrgZombies(t *testing.T) {
	mk := func(repo, wf string, fails int, span float64) OrgZombieCron {
		return OrgZombieCron{Repo: repo, Workflow: wf, Fails: fails, SpanDays: span}
	}
	zs := []OrgZombieCron{
		mk("b", "x", 5, 3),
		mk("a", "y", 20, 10),
		mk("a", "x", 5, 3), // ties with b/x on fails+span: repo breaks the tie
		mk("c", "z", 5, 9),
	}
	got, more := finishOrgZombies(zs)
	if more != 0 {
		t.Errorf("more = %d, want 0", more)
	}
	order := []string{"a/y", "c/z", "a/x", "b/x"}
	for i, want := range order {
		if key := got[i].Repo + "/" + got[i].Workflow; key != want {
			t.Errorf("order[%d] = %s, want %s (full: %+v)", i, key, want, got)
		}
	}

	// Cap: 12 entries → 10 rendered + 2 counted.
	var many []OrgZombieCron
	for i := 0; i < 12; i++ {
		many = append(many, mk(fmt.Sprintf("r%02d", i), "w", 5+i, 4))
	}
	got, more = finishOrgZombies(many)
	if len(got) != 10 || more != 2 {
		t.Errorf("cap: len=%d more=%d, want 10/2", len(got), more)
	}
	if got[0].Fails != 16 {
		t.Errorf("cap kept the wrong end: got[0].Fails=%d, want 16 (longest streak)", got[0].Fails)
	}
}
