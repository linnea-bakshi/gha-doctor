package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ts prefixes each line with a runner-style timestamp.
func logts(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("2026-07-26T22:06:25.5703373Z " + l + "\n")
	}
	return b.String()
}

func TestParseCacheLogActionsCacheExactHit(t *testing.T) {
	log := logts(
		"##[group]Run actions/cache@v4",
		"with:",
		"  path: ~/go/pkg/mod",
		"  key: Linux-go-4ae0e4f812345678",
		"##[endgroup]",
		"Cache Size: ~201 MB (210798569 B)",
		"Cache restored from key: Linux-go-4ae0e4f812345678",
	)
	evs := parseCacheLog(log)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d: %+v", len(evs), evs)
	}
	if evs[0].kind != evHit {
		t.Errorf("want exact hit, got kind %d", evs[0].kind)
	}
	if evs[0].sizeMB < 200 || evs[0].sizeMB > 202 {
		t.Errorf("size = %.1f MB, want ~201", evs[0].sizeMB)
	}
}

func TestParseCacheLogPartialViaRestoreKeys(t *testing.T) {
	log := logts(
		"##[group]Run actions/cache@v4",
		"with:",
		"  key: Linux-go-newhash11223344",
		"  restore-keys: Linux-go-",
		"##[endgroup]",
		"Cache restored from key: Linux-go-oldhash55667788",
	)
	evs := parseCacheLog(log)
	if len(evs) != 1 || evs[0].kind != evPartial {
		t.Fatalf("want 1 partial event, got %+v", evs)
	}
}

func TestParseCacheLogMissWithInputKeys(t *testing.T) {
	log := logts(
		"##[group]Run actions/cache@v4",
		"with:",
		"  key: Linux-npm-abc123def4567890",
		"##[endgroup]",
		"Cache not found for input keys: Linux-npm-abc123def4567890, Linux-npm-",
	)
	evs := parseCacheLog(log)
	if len(evs) != 1 || evs[0].kind != evMiss {
		t.Fatalf("want 1 miss, got %+v", evs)
	}
	if evs[0].key != "Linux-npm-abc123def4567890" {
		t.Errorf("miss key = %q", evs[0].key)
	}
}

// Real-world setup-go vocabulary: "Cache is not found" (miss), then
// "Cache saved with the key: K" in the post step names the miss.
func TestParseCacheLogSetupGoMissThenSave(t *testing.T) {
	log := logts(
		"##[group]Run actions/setup-go@v5",
		"with:",
		"  go-version: 1.24",
		"  cache: true",
		"##[endgroup]",
		"Cache is not found",
		"##[group]Run go test ./...",
		"##[endgroup]",
		"ok  	example.com/pkg	0.5s",
		"Post job cleanup.",
		"Cache saved with the key: setup-go-Linux-x64-ubuntu24-go-1.24.13-73c9e98d3182489b",
	)
	evs := parseCacheLog(log)
	if len(evs) != 2 {
		t.Fatalf("want miss+save, got %+v", evs)
	}
	if evs[0].kind != evMiss || evs[1].kind != evSave {
		t.Fatalf("kinds = %d,%d", evs[0].kind, evs[1].kind)
	}
	if evs[0].key != "setup-go-Linux-x64-ubuntu24-go-1.24.13-73c9e98d3182489b" {
		t.Errorf("miss not named by later save: key=%q", evs[0].key)
	}
}

// Real-world setup-go hit: "Cache hit for: K" followed by size and
// "Cache restored from key: K" — one event, not three.
func TestParseCacheLogHitForThenRestoredDedup(t *testing.T) {
	log := logts(
		"##[group]Run actions/setup-go@v5",
		"with:",
		"  cache: true",
		"##[endgroup]",
		"Cache hit for: setup-go-Linux-x64-go-1.26.5-f45f767aa5e2b519",
		"Cache Size: ~201 MB (210798569 B)",
		"Cache restored successfully",
		"Cache restored from key: setup-go-Linux-x64-go-1.26.5-f45f767aa5e2b519",
	)
	evs := parseCacheLog(log)
	if len(evs) != 1 {
		t.Fatalf("want 1 deduped hit, got %d: %+v", len(evs), evs)
	}
	if evs[0].kind != evHit || evs[0].sizeMB < 200 {
		t.Errorf("event = %+v", evs[0])
	}
}

func TestParseCacheLogSaveConflict(t *testing.T) {
	log := logts(
		"##[group]Run actions/cache@v4",
		"with:",
		"  key: k-112233445566aabb",
		"##[endgroup]",
		"Cache not found for input keys: k-112233445566aabb",
		"Post job cleanup.",
		"Failed to save: Unable to reserve cache with key k-112233445566aabb, another job may be creating this cache.",
	)
	evs := parseCacheLog(log)
	if len(evs) != 2 || evs[1].kind != evConflict {
		t.Fatalf("want miss+conflict, got %+v", evs)
	}
}

// A second step group must reset the primary key from the first.
func TestParseCacheLogSectionReset(t *testing.T) {
	log := logts(
		"##[group]Run actions/cache@v4",
		"with:",
		"  key: first-key-aabbccdd11223344",
		"##[endgroup]",
		"Cache restored from key: first-key-aabbccdd11223344",
		"##[group]Run actions/cache@v4",
		"with:",
		"  key: second-key-9988776655443322",
		"##[endgroup]",
		"Cache restored from key: second-key-9988776655443322",
	)
	evs := parseCacheLog(log)
	if len(evs) != 2 || evs[0].kind != evHit || evs[1].kind != evHit {
		t.Fatalf("want 2 exact hits, got %+v", evs)
	}
}

func TestNormalizeCacheKey(t *testing.T) {
	cases := map[string]string{
		"Linux-go-4ae0e4f83182489b5cee8f1ae086fc9d":                                                               "Linux-go-*",
		"setup-go-Linux-x64-ubuntu24-go-1.24.13-73c9e98d3182489b5cee8f1ae086fc9de99502590301e15352d8cdf9fcbfd0f2": "setup-go-Linux-x64-ubuntu24-go-1.24.13-*",
		"golangci-lint.cache-Linux-2951-659cf3a1503bbaf4ac33d10f94ff5644f209f370":                                 "golangci-lint.cache-Linux-2951-*",
		"npm-123456789-abc": "npm-*-abc",
		"plain-key":         "plain-key",
	}
	for in, want := range cases {
		if got := normalizeCacheKey(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickLogSampleJobsRoundRobin(t *testing.T) {
	now := time.Now()
	jobsByRun := map[int64][]Job{}
	// 10 runs of a chatty job "build", 2 runs of "lint".
	for i := 0; i < 10; i++ {
		jobsByRun[int64(i)] = []Job{{ID: int64(100 + i), Name: "build", Status: "completed", Conclusion: "success", CreatedAt: now.Add(-time.Duration(i) * time.Hour)}}
	}
	jobsByRun[20] = []Job{{ID: 200, Name: "lint", Status: "completed", Conclusion: "success", CreatedAt: now}}
	jobsByRun[21] = []Job{{ID: 201, Name: "lint", Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-time.Hour)}}
	jobsByRun[22] = []Job{{ID: 300, Name: "skipped-one", Status: "completed", Conclusion: "skipped", CreatedAt: now}}

	got := pickLogSampleJobs(jobsByRun, 4)
	if len(got) != 4 {
		t.Fatalf("want 4 jobs, got %d", len(got))
	}
	names := map[string]int{}
	for _, j := range got {
		names[j.Name]++
		if j.Conclusion == "skipped" {
			t.Errorf("sampled a skipped job")
		}
	}
	if names["lint"] != 2 || names["build"] != 2 {
		t.Errorf("round-robin failed: %v", names)
	}
}

func TestAnalyzeCacheLogsEndToEnd(t *testing.T) {
	hitLog := logts(
		"##[group]Run actions/cache@v4",
		"with:",
		"  key: Linux-go-aabb00112233",
		"##[endgroup]",
		"Cache Size: ~100 MB (104857600 B)",
		"Cache restored from key: Linux-go-aabb00112233",
	)
	missLog := logts(
		"##[group]Run actions/cache@v4",
		"with:",
		"  key: Linux-go-ffee99887766",
		"##[endgroup]",
		"Cache not found for input keys: Linux-go-ffee99887766, Linux-go-",
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/jobs/1/logs"):
			fmt.Fprint(w, hitLog)
		case strings.HasSuffix(r.URL.Path, "/jobs/2/logs"):
			fmt.Fprint(w, missLog)
		case strings.HasSuffix(r.URL.Path, "/jobs/3/logs"):
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{Token: "t", BaseURL: srv.URL, HTTP: srv.Client()}
	now := time.Now()
	jobsByRun := map[int64][]Job{
		10: {{ID: 1, Name: "a", Status: "completed", Conclusion: "success", CreatedAt: now}},
		11: {{ID: 2, Name: "b", Status: "completed", Conclusion: "success", CreatedAt: now}},
		12: {{ID: 3, Name: "c", Status: "completed", Conclusion: "success", CreatedAt: now}},
	}
	st := c.analyzeCacheLogs("o", "r", jobsByRun, 10, func(string) {})
	if !st.Available {
		t.Fatalf("not available: %s", st.Note)
	}
	if st.JobsSampled != 2 || st.JobsSkipped != 1 {
		t.Errorf("sampled=%d skipped=%d", st.JobsSampled, st.JobsSkipped)
	}
	if st.Restores != 2 || st.Hits != 1 || st.Misses != 1 {
		t.Errorf("restores=%d hits=%d misses=%d", st.Restores, st.Hits, st.Misses)
	}
	if st.HitRate != 50 {
		t.Errorf("hit rate = %.0f, want 50", st.HitRate)
	}
	if len(st.Groups) != 1 || st.Groups[0].Pattern != "Linux-go-*" {
		t.Errorf("groups = %+v", st.Groups)
	}
	if st.RestoredMB < 99 || st.RestoredMB > 101 {
		t.Errorf("restored MB = %.1f", st.RestoredMB)
	}
}

func TestAnalyzeCacheLogsNoToken(t *testing.T) {
	c := &Client{Token: ""}
	st := c.analyzeCacheLogs("o", "r", map[int64][]Job{}, 5, func(string) {})
	if st.Available {
		t.Error("should be unavailable without a token")
	}
	if !strings.Contains(st.Note, "auth") {
		t.Errorf("note = %q", st.Note)
	}
}

func trendSample(daysAgo int, restores, effHits int) jobCacheSample {
	return jobCacheSample{
		t:        time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -daysAgo),
		restores: restores,
		effHits:  effHits,
	}
}

func TestComputeCacheTrendDegrading(t *testing.T) {
	samples := []jobCacheSample{
		// older half: 12 restores, all hits
		trendSample(9, 6, 6), trendSample(8, 6, 6),
		// newer half: 12 restores, 3 hits
		trendSample(2, 6, 2), trendSample(1, 6, 1),
	}
	tr := computeCacheTrend(samples)
	if tr == nil {
		t.Fatal("expected a trend")
	}
	if tr.OlderRestores != 12 || tr.NewerRestores != 12 {
		t.Errorf("restores older=%d newer=%d", tr.OlderRestores, tr.NewerRestores)
	}
	if tr.OlderHitRate != 100 || tr.NewerHitRate != 25 {
		t.Errorf("rates older=%.0f newer=%.0f", tr.OlderHitRate, tr.NewerHitRate)
	}
	if tr.DeltaPts != -75 {
		t.Errorf("delta = %.0f, want -75", tr.DeltaPts)
	}
	if !tr.OlderFrom.Before(tr.NewerFrom) || !tr.OlderTo.Before(tr.NewerTo) {
		t.Errorf("half boundaries out of order: %+v", tr)
	}
}

func TestComputeCacheTrendSortsUnorderedInput(t *testing.T) {
	samples := []jobCacheSample{
		trendSample(1, 6, 6), trendSample(9, 6, 0),
		trendSample(2, 6, 6), trendSample(8, 6, 0),
	}
	tr := computeCacheTrend(samples)
	if tr == nil {
		t.Fatal("expected a trend")
	}
	if tr.OlderHitRate != 0 || tr.NewerHitRate != 100 {
		t.Errorf("rates older=%.0f newer=%.0f — input not sorted by time?", tr.OlderHitRate, tr.NewerHitRate)
	}
}

func TestComputeCacheTrendNilWhenSpanTooShort(t *testing.T) {
	samples := []jobCacheSample{
		trendSample(0, 10, 10), trendSample(0, 10, 10),
		trendSample(0, 10, 10), trendSample(0, 10, 10),
	}
	if tr := computeCacheTrend(samples); tr != nil {
		t.Errorf("same-day sample produced a trend: %+v", tr)
	}
}

func TestComputeCacheTrendNilWhenHalvesTooSmall(t *testing.T) {
	samples := []jobCacheSample{
		trendSample(9, 4, 4), trendSample(8, 4, 4),
		trendSample(2, 4, 4), trendSample(1, 4, 4),
	}
	if tr := computeCacheTrend(samples); tr != nil {
		t.Errorf("8-restore halves produced a trend: %+v", tr)
	}
}

func TestComputeCacheTrendIgnoresJobsWithoutActivity(t *testing.T) {
	samples := []jobCacheSample{
		trendSample(9, 10, 10), trendSample(8, 10, 10),
		trendSample(5, 0, 0), trendSample(5, 0, 0), trendSample(5, 0, 0),
		trendSample(2, 10, 0), trendSample(1, 10, 0),
	}
	tr := computeCacheTrend(samples)
	if tr == nil {
		t.Fatal("expected a trend")
	}
	if tr.OlderRestores+tr.NewerRestores != 40 {
		t.Errorf("no-activity jobs leaked into the split: %+v", tr)
	}
}
