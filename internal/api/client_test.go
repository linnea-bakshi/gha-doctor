package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(handler http.Handler) (*Client, *httptest.Server) {
	srv := httptest.NewServer(handler)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client()}, srv
}

func TestListRunsPagination(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("status"); got != "" {
			t.Errorf("status param = %q, want none (server-side status filter serves a stale index; filter client-side)", got)
		}
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			// Full page of 100 -> client must fetch page 2.
			fmt.Fprint(w, `{"workflow_runs":[`)
			for i := 0; i < 100; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"id":%d,"name":"CI","status":"completed"}`, i+1)
			}
			fmt.Fprint(w, `]}`)
			return
		}
		fmt.Fprint(w, `{"workflow_runs":[{"id":101,"name":"CI","status":"completed"}]}`)
	}))
	defer srv.Close()

	runs, err := c.ListRuns("o", "r", 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 101 {
		t.Errorf("got %d runs, want 101", len(runs))
	}
}

func TestListRunsRespectsMax(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// per_page must stay pinned at 100: GitHub's offset is
		// page*per_page, so shrinking it for a partial page re-reads
		// earlier items instead of continuing.
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want 100 (always)", got)
		}
		fmt.Fprint(w, `{"workflow_runs":[{"id":1,"name":"CI","status":"completed"}]}`)
	}))
	defer srv.Close()
	runs, err := c.ListRuns("o", "r", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("got %d runs, want 1", len(runs))
	}
}

// TestListRunsPartialLastPage simulates GitHub's page*per_page offset
// semantics with 260 runs and asks for 250. The pre-fix client shrank
// per_page to 50 for the third request, which made GitHub serve items
// 101-150 again: 50 duplicates and the oldest 50 runs never fetched
// (reproduced live against cli/cli). The fixed client must return 250
// distinct runs in order.
func TestListRunsPartialLastPage(t *testing.T) {
	const total = 260
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		per := 0
		page := 0
		fmt.Sscan(q.Get("per_page"), &per)
		fmt.Sscan(q.Get("page"), &page)
		// Real GitHub offset math: items are 1..total, newest first.
		start := (page-1)*per + 1
		fmt.Fprint(w, `{"workflow_runs":[`)
		n := 0
		for i := start; i <= total && i < start+per; i++ {
			if n > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"name":"CI","status":"completed"}`, i)
			n++
		}
		fmt.Fprint(w, `]}`)
	}))
	defer srv.Close()

	runs, err := c.ListRuns("o", "r", 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 250 {
		t.Fatalf("got %d runs, want 250", len(runs))
	}
	seen := make(map[int64]bool)
	for i, r := range runs {
		if seen[r.ID] {
			t.Fatalf("duplicate run ID %d at index %d", r.ID, i)
		}
		seen[r.ID] = true
		if want := int64(i + 1); r.ID != want {
			t.Fatalf("runs[%d].ID = %d, want %d (order broken)", i, r.ID, want)
		}
	}
}

// TestListRunsDedupOnPageShift simulates a busy repo where a new run lands
// between page fetches: page 2 re-serves the last item of page 1 because
// every offset shifted by one. The duplicate must be dropped, not counted.
func TestListRunsDedupOnPageShift(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "1" {
			fmt.Fprint(w, `{"workflow_runs":[`)
			for i := 0; i < 100; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"id":%d,"name":"CI","status":"completed"}`, 1000-i)
			}
			fmt.Fprint(w, `]}`)
			return
		}
		// A new run pushed everything down one: page 2 starts with the
		// run that was last on page 1 (id 901), then continues 900, 899.
		fmt.Fprint(w, `{"workflow_runs":[{"id":901,"name":"CI","status":"completed"},{"id":900,"name":"CI","status":"completed"},{"id":899,"name":"CI","status":"completed"}]}`)
	}))
	defer srv.Close()

	runs, err := c.ListRuns("o", "r", 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 102 {
		t.Fatalf("got %d runs, want 102 (100 + 2 new after dedup)", len(runs))
	}
	ids := make(map[int64]bool)
	for _, r := range runs {
		if ids[r.ID] {
			t.Fatalf("duplicate run ID %d survived dedup", r.ID)
		}
		ids[r.ID] = true
	}
}

// TestListRunsFiltersClientSide: queued/in-progress runs in the unfiltered
// listing must be skipped locally, and the next page fetched to fill the
// requested count. Server-side status filtering is off-limits — GitHub
// serves those from an index whose replicas were observed weeks stale.
func TestListRunsFiltersClientSide(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("status"); got != "" {
			t.Errorf("status param = %q, want none", got)
		}
		page := r.URL.Query().Get("page")
		if page == "1" {
			// 100 runs, the newest 97 still in flight (busy repo mid-burst).
			fmt.Fprint(w, `{"workflow_runs":[`)
			for i := 0; i < 100; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				status := "completed"
				if i < 97 {
					status = []string{"queued", "in_progress", "pending"}[i%3]
				}
				fmt.Fprintf(w, `{"id":%d,"name":"CI","status":%q}`, 1000-i, status)
			}
			fmt.Fprint(w, `]}`)
			return
		}
		fmt.Fprint(w, `{"workflow_runs":[{"id":900,"name":"CI","status":"completed"},{"id":899,"name":"CI","status":"completed"}]}`)
	}))
	defer srv.Close()

	runs, err := c.ListRuns("o", "r", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 5 {
		t.Fatalf("got %d runs, want 5 (3 from page 1 + 2 from page 2)", len(runs))
	}
	for _, r := range runs {
		if r.Status != "completed" {
			t.Fatalf("run %d has status %q, want completed", r.ID, r.Status)
		}
	}
}

// TestListRunsPageCap: an endless wall of in-progress runs must not turn
// into an unbounded crawl — the client stops after max/100+3 pages.
func TestListRunsPageCap(t *testing.T) {
	pages := 0
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		page := 0
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		fmt.Fprint(w, `{"workflow_runs":[`)
		for i := 0; i < 100; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"name":"CI","status":"queued"}`, page*1000+i)
		}
		fmt.Fprint(w, `]}`)
	}))
	defer srv.Close()

	runs, err := c.ListRuns("o", "r", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("got %d runs, want 0", len(runs))
	}
	if pages > 3 {
		t.Fatalf("fetched %d pages, want <= 3 (max/100+3)", pages)
	}
}

// TestListRunsSpeculativePageFailureIgnored: the first wave fetches one
// page beyond the filtered minimum in parallel. If that speculative page
// fails but the earlier pages already satisfied the request, the error
// must not surface — we never needed its data.
func TestListRunsSpeculativePageFailureIgnored(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"workflow_runs":[`)
		for i := 0; i < 100; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"name":"CI","status":"completed"}`, i+1)
		}
		fmt.Fprint(w, `]}`)
	}))
	defer srv.Close()

	runs, err := c.ListRuns("o", "r", 100)
	if err != nil {
		t.Fatalf("speculative page failure surfaced: %v", err)
	}
	if len(runs) != 100 {
		t.Fatalf("got %d runs, want 100", len(runs))
	}

	// But when the failed page's data IS needed, the error must surface.
	if _, err := c.ListRuns("o", "r", 150); err == nil {
		t.Fatal("needed-page failure did not surface")
	}
}

func TestListJobsIncludesAllAttempts(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("filter"); got != "all" {
			t.Errorf("filter = %q, want all", got)
		}
		fmt.Fprint(w, `{"jobs":[{"id":1,"name":"test","run_attempt":1},{"id":2,"name":"test","run_attempt":2}]}`)
	}))
	defer srv.Close()
	jobs, err := c.ListJobs("o", "r", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}
}

func TestRateLimitError(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1752000000")
		w.WriteHeader(403)
	}))
	defer srv.Close()
	_, err := c.ListRuns("o", "r", 10)
	if err == nil {
		t.Fatal("want rate-limit error, got nil")
	}
	want := "rate limit"
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error %q does not mention %q", got, want)
	}
}

func TestHTTPErrorIncludesStatus(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, 404)
	}))
	defer srv.Close()
	_, err := c.ListRuns("o", "r", 10)
	if err == nil {
		t.Fatal("want 404 error, got nil")
	}
	if !contains(err.Error(), "404") {
		t.Errorf("error %q does not mention 404", err)
	}
}

func TestAnalyzeEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"workflow_runs":[
			{"id":1,"name":"CI","status":"completed","head_sha":"abc","conclusion":"failure","run_attempt":1,
			 "run_started_at":%[1]q,"created_at":%[1]q,"updated_at":%[2]q},
			{"id":2,"name":"CI","status":"completed","head_sha":"abc","conclusion":"success","run_attempt":1,
			 "run_started_at":%[1]q,"created_at":%[1]q,"updated_at":%[2]q}
		]}`, ts(0), ts(10))
	})
	mux.HandleFunc("/repos/o/r/actions/runs/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"jobs":[{"id":11,"run_id":1,"run_attempt":1,"name":"test","conclusion":"failure",
			"created_at":%q,"started_at":%q,"completed_at":%q,"labels":["ubuntu-latest"]}]}`,
			ts(0), ts(1), ts(9))
	})
	mux.HandleFunc("/repos/o/r/actions/runs/2/jobs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"jobs":[{"id":21,"run_id":2,"run_attempt":1,"name":"test","conclusion":"success",
			"created_at":%q,"started_at":%q,"completed_at":%q,"labels":["ubuntu-latest"]}]}`,
			ts(0), ts(1), ts(9))
	})
	c, srv := testClient(mux)
	defer srv.Close()

	a, err := c.Analyze("o", "r", 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.RunsSampled != 2 {
		t.Errorf("RunsSampled = %d, want 2", a.RunsSampled)
	}
	if len(a.FlakyJobs) != 1 {
		t.Fatalf("FlakyJobs = %+v, want exactly one (fail+pass on same SHA)", a.FlakyJobs)
	}
	if a.FlakyJobs[0].Job != "test" {
		t.Errorf("flaky job = %q, want test", a.FlakyJobs[0].Job)
	}
}

func ts(minutes int) string {
	return time.Date(2026, 7, 1, 12, minutes, 0, 0, time.UTC).Format(time.RFC3339)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestListCachesPagination(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/actions/caches") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			fmt.Fprint(w, `{"total_count":101,"actions_caches":[`)
			for i := 0; i < 100; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"id":%d,"key":"k%d","ref":"refs/heads/main","size_in_bytes":1048576}`, i+1, i+1)
			}
			fmt.Fprint(w, `]}`)
			return
		}
		fmt.Fprint(w, `{"total_count":101,"actions_caches":[{"id":101,"key":"k101","ref":"refs/pull/9/merge","size_in_bytes":2097152}]}`)
	}))
	defer srv.Close()

	caches, truncated, err := c.ListCaches("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("101 caches fit within the page cap; want truncated=false")
	}
	if len(caches) != 101 {
		t.Fatalf("got %d caches, want 101", len(caches))
	}
	if caches[100].Ref != "refs/pull/9/merge" || caches[100].SizeInBytes != 2097152 {
		t.Errorf("last cache = %+v", caches[100])
	}
}

func TestListCachesUnauthorized(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Must have actions:read"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, _, err := c.ListCaches("o", "r"); err == nil {
		t.Fatal("want error on 401")
	}
}

func TestRateLimitErrorIsTyped(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(403)
	}))
	defer srv.Close()
	_, _, err := c.ListCaches("o", "r")
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("want *RateLimitError, got %T: %v", err, err)
	}
}

func TestListCachesPageCap(t *testing.T) {
	// A repo with 100k caches (nodejs/node has 137k) must not trigger a
	// 1,000-page walk: stop at maxCachePages and report truncation.
	var pagesServed int
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagesServed++
		if got := r.URL.Query().Get("direction"); got != "desc" {
			t.Errorf("direction param = %q, want desc (largest first)", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"total_count":100000,"actions_caches":[`)
		for i := 0; i < 100; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"key":"k%d","ref":"refs/heads/main","size_in_bytes":1048576}`, i+1, i+1)
		}
		fmt.Fprint(w, `]}`)
	}))
	defer srv.Close()

	caches, truncated, err := c.ListCaches("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if pagesServed != 3 {
		t.Errorf("served %d pages, want exactly maxCachePages=3", pagesServed)
	}
	if !truncated {
		t.Error("100k total with 300 fetched: want truncated=true")
	}
	if len(caches) != 300 {
		t.Errorf("got %d caches, want 300", len(caches))
	}
}

func TestCacheUsage(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/actions/cache/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"full_name":"o/r","active_caches_size_in_bytes":21478178088,"active_caches_count":137799}`)
	}))
	defer srv.Close()

	size, count, err := c.CacheUsage("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if size != 21478178088 || count != 137799 {
		t.Errorf("got size=%d count=%d", size, count)
	}
}

func TestAnalyzeSampledCacheGetsExactTotals(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"workflow_runs":[
			{"id":1,"name":"CI","status":"completed","head_sha":"abc","conclusion":"success","run_attempt":1,
			 "run_started_at":%[1]q,"created_at":%[1]q,"updated_at":%[2]q}
		]}`, ts(0), ts(10))
	})
	mux.HandleFunc("/repos/o/r/actions/runs/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jobs":[]}`)
	})
	mux.HandleFunc("/repos/o/r/actions/caches", func(w http.ResponseWriter, r *http.Request) {
		// Every page full => truncated after maxCachePages.
		fmt.Fprint(w, `{"total_count":100000,"actions_caches":[`)
		for i := 0; i < 100; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"key":"k%d","ref":"refs/pull/1/merge","size_in_bytes":1048576}`, i+1, i+1)
		}
		fmt.Fprint(w, `]}`)
	})
	mux.HandleFunc("/repos/o/r/actions/cache/usage", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"active_caches_size_in_bytes":10737418240,"active_caches_count":100000}`)
	})
	c, srv := testClient(mux)
	defer srv.Close()

	a, err := c.Analyze("o", "r", 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := a.Cache
	if !cs.Available || !cs.Sampled {
		t.Fatalf("cache stats = %+v, want Available && Sampled", cs)
	}
	if cs.SampleCount != 300 {
		t.Errorf("SampleCount = %d, want 300", cs.SampleCount)
	}
	// Exact totals from the usage endpoint, not the truncated sum.
	if cs.Count != 100000 {
		t.Errorf("Count = %d, want 100000 (from cache/usage)", cs.Count)
	}
	if cs.TotalMB != 10240 {
		t.Errorf("TotalMB = %.0f, want 10240", cs.TotalMB)
	}
	if cs.LimitPct != 100 {
		t.Errorf("LimitPct = %.0f, want 100", cs.LimitPct)
	}
	// Breakdown still computed over the sample.
	if cs.PRRefCount != 300 {
		t.Errorf("PRRefCount = %d, want 300", cs.PRRefCount)
	}
}

func TestListArtifactsPagination(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/actions/artifacts") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			fmt.Fprint(w, `{"total_count":101,"artifacts":[`)
			for i := 0; i < 100; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"id":%d,"name":"a%d","size_in_bytes":1048576,"expired":false,"created_at":"2026-07-01T00:00:00Z","expires_at":"2026-09-29T00:00:00Z"}`, i+1, i+1)
			}
			fmt.Fprint(w, `]}`)
			return
		}
		fmt.Fprint(w, `{"total_count":101,"artifacts":[{"id":101,"name":"tail","size_in_bytes":2097152,"expired":true,"created_at":"2026-06-01T00:00:00Z","expires_at":"2026-06-08T00:00:00Z"}]}`)
	}))
	defer srv.Close()

	arts, total, truncated, err := c.ListArtifacts("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("101 artifacts fit within the page cap; want truncated=false")
	}
	if total != 101 || len(arts) != 101 {
		t.Fatalf("got %d/%d artifacts, want 101/101", len(arts), total)
	}
	if arts[100].Name != "tail" || !arts[100].Expired {
		t.Errorf("last artifact = %+v", arts[100])
	}
}

func TestListArtifactsPageCap(t *testing.T) {
	// Busy repos hold five-figure artifact counts; stop at
	// maxArtifactPages and report truncation.
	var pagesServed int
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagesServed++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"total_count":50000,"artifacts":[`)
		for i := 0; i < 100; i++ {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"id":%d,"name":"a","size_in_bytes":1,"created_at":"2026-07-01T00:00:00Z","expires_at":"2026-09-29T00:00:00Z"}`, i+1)
		}
		fmt.Fprint(w, `]}`)
	}))
	defer srv.Close()

	arts, total, truncated, err := c.ListArtifacts("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if pagesServed != maxArtifactPages {
		t.Errorf("served %d pages, want exactly %d", pagesServed, maxArtifactPages)
	}
	if !truncated || total != 50000 || len(arts) != maxArtifactPages*100 {
		t.Errorf("arts=%d total=%d truncated=%v", len(arts), total, truncated)
	}
}
