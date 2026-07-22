package api

import (
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
		if got := r.URL.Query().Get("status"); got != "completed" {
			t.Errorf("status param = %q, want completed", got)
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
				fmt.Fprintf(w, `{"id":%d,"name":"CI"}`, i+1)
			}
			fmt.Fprint(w, `]}`)
			return
		}
		fmt.Fprint(w, `{"workflow_runs":[{"id":101,"name":"CI"}]}`)
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
		if got := r.URL.Query().Get("per_page"); got != "30" {
			t.Errorf("per_page = %q, want 30 (capped at max)", got)
		}
		fmt.Fprint(w, `{"workflow_runs":[{"id":1,"name":"CI"}]}`)
	}))
	defer srv.Close()
	if _, err := c.ListRuns("o", "r", 30); err != nil {
		t.Fatal(err)
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
			{"id":1,"name":"CI","head_sha":"abc","conclusion":"failure","run_attempt":1,
			 "run_started_at":%[1]q,"created_at":%[1]q,"updated_at":%[2]q},
			{"id":2,"name":"CI","head_sha":"abc","conclusion":"success","run_attempt":1,
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

	caches, err := c.ListCaches("o", "r")
	if err != nil {
		t.Fatal(err)
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

	if _, err := c.ListCaches("o", "r"); err == nil {
		t.Fatal("want error on 401")
	}
}
