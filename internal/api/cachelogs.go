package api

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CacheLogStats measures the actual cache hit/miss rate by sampling job logs.
// The Actions API exposes cache *contents* but not hit rates; the only place
// a hit or miss is recorded is the log text itself. One API request per
// sampled job, so this is opt-in (--cache-logs N).
type CacheLogStats struct {
	Available   bool   `json:"available"`
	Note        string `json:"note,omitempty"`
	JobsSampled int    `json:"jobs_sampled"`
	JobsSkipped int    `json:"jobs_skipped"` // logs expired or fetch failed

	Restores    int     `json:"restores"`
	Hits        int     `json:"hits"`
	PartialHits int     `json:"partial_hits"` // restored via restore-keys, not the primary key
	Misses      int     `json:"misses"`
	HitRate     float64 `json:"hit_rate"` // (hits+partial)/restores

	Saves         int `json:"saves"`
	SaveConflicts int `json:"save_conflicts"` // "unable to reserve cache" races

	RestoredMB float64 `json:"restored_mb"` // total cache bytes downloaded across sample

	Groups []CacheKeyGroup `json:"groups,omitempty"`
	Trend  *CacheTrend     `json:"trend,omitempty"`
}

// CacheTrend compares the hit rate of the older half of the sampled jobs
// against the newer half, so a degrading cache shows up before it becomes
// "the build got slow last month". Only reported when both halves have
// enough restores and the sample spans enough wall-clock time to mean
// something (see trendMinRestores / trendMinSpan).
type CacheTrend struct {
	OlderFrom     time.Time `json:"older_from"`
	OlderTo       time.Time `json:"older_to"`
	NewerFrom     time.Time `json:"newer_from"`
	NewerTo       time.Time `json:"newer_to"`
	OlderRestores int       `json:"older_restores"`
	NewerRestores int       `json:"newer_restores"`
	OlderHitRate  float64   `json:"older_hit_rate"` // percent, hits+partial
	NewerHitRate  float64   `json:"newer_hit_rate"`
	DeltaPts      float64   `json:"delta_pts"` // newer - older, percentage points
}

// CacheKeyGroup aggregates restore events whose keys normalize to the same
// pattern (hashes and long numbers replaced with '*').
type CacheKeyGroup struct {
	Pattern  string  `json:"pattern"`
	Restores int     `json:"restores"`
	Hits     int     `json:"hits"`
	Partial  int     `json:"partial"`
	Misses   int     `json:"misses"`
	HitPct   float64 `json:"hit_pct"`
	AvgMB    float64 `json:"avg_mb"` // average size of successful restores
}

type cacheEventKind int

const (
	evHit cacheEventKind = iota
	evPartial
	evMiss
	evSave
	evConflict
)

type cacheEvent struct {
	kind    cacheEventKind
	key     string  // raw key (restored key for hits, primary for misses)
	primary string  // requested primary key when known ("" otherwise)
	section string  // action that produced it, e.g. "actions/setup-go"
	sizeMB  float64 // for hits/saves when the log printed Cache Size
}

// Log line markers, in the wild (verified against real runner logs):
//
//	actions/cache & toolkit:  "Cache restored from key: K"
//	                          "Cache not found for input keys: K1, K2"
//	                          "Cache saved with key: K"
//	setup-go:                 "Cache is not found"
//	                          "Cache saved with the key: K"
//	setup-go/setup-node etc.: "Cache hit for: K"
//	toolkit (restore-keys):   "Cache hit for restore-keys: K"
//	toolkit (save race):      "Unable to reserve cache with key"
//	toolkit (size):           "Cache Size: ~201 MB (210798569 B)"
var (
	reTS         = regexp.MustCompile(`^\S+ `) // timestamp prefix on every line
	reGroupRun   = regexp.MustCompile(`^##\[group\]Run ([^\s@]+)`)
	rePrimaryKey = regexp.MustCompile(`^\s{2}key: (.+)$`)
	reRestored   = regexp.MustCompile(`^Cache restored from key: (.+)$`)
	reHitFor     = regexp.MustCompile(`^Cache hit for: (.+)$`)
	reHitRestore = regexp.MustCompile(`^Cache hit for restore-keys?: (.+)$`)
	reMissKeys   = regexp.MustCompile(`^Cache not found for (?:input )?keys?: (.+)$`)
	reMissPlain  = regexp.MustCompile(`^Cache is not found\b`)
	reSaved      = regexp.MustCompile(`^Cache saved with (?:the )?key: (.+)$`)
	reConflict   = regexp.MustCompile(`Unable to reserve cache with key`)
	reSize       = regexp.MustCompile(`^Cache Size: ~\d+ MB \((\d+) B\)`)
	reHex        = regexp.MustCompile(`(?i)[0-9a-f]{8,}`)
	reLongNum    = regexp.MustCompile(`\d{6,}`)
)

// parseCacheLog extracts cache restore/save events from one job's log text.
func parseCacheLog(text string) []cacheEvent {
	var events []cacheEvent
	var section, primary string
	var pendingSize float64
	inWith := false
	// index of a tentative "Cache hit for:" event awaiting the more precise
	// "Cache restored from key:" line in the same section; -1 = none.
	tentative := -1

	flushSection := func(newSection string) {
		section, primary, pendingSize, inWith, tentative = newSection, "", 0, false, -1
	}

	for _, raw := range strings.Split(text, "\n") {
		line := reTS.ReplaceAllString(strings.TrimRight(raw, "\r"), "")
		if m := reGroupRun.FindStringSubmatch(line); m != nil {
			flushSection(m[1])
			inWith = true // the with: block follows immediately
			continue
		}
		if strings.HasPrefix(line, "##[endgroup]") {
			inWith = false
			continue
		}
		if inWith {
			if m := rePrimaryKey.FindStringSubmatch(line); m != nil {
				primary = strings.TrimSpace(m[1])
			}
			continue
		}
		switch {
		case reSize.MatchString(line):
			if m := reSize.FindStringSubmatch(line); m != nil {
				if b, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					pendingSize = float64(b) / (1 << 20)
				}
			}
		case reRestored.MatchString(line):
			key := reRestored.FindStringSubmatch(line)[1]
			ev := classifyHit(key, primary, section, pendingSize)
			if tentative >= 0 {
				if ev.sizeMB == 0 {
					ev.sizeMB = events[tentative].sizeMB
				}
				events[tentative] = ev // upgrade: same event, better key info
			} else {
				events = append(events, ev)
			}
			tentative = -1
			pendingSize = 0
		case reHitRestore.MatchString(line):
			key := reHitRestore.FindStringSubmatch(line)[1]
			events = append(events, cacheEvent{kind: evPartial, key: key, primary: primary, section: section, sizeMB: pendingSize})
			tentative = len(events) - 1
			pendingSize = 0
		case reHitFor.MatchString(line):
			key := reHitFor.FindStringSubmatch(line)[1]
			events = append(events, classifyHit(key, primary, section, pendingSize))
			tentative = len(events) - 1
			pendingSize = 0
		case reMissKeys.MatchString(line):
			keys := reMissKeys.FindStringSubmatch(line)[1]
			first := strings.TrimSpace(strings.SplitN(keys, ",", 2)[0])
			events = append(events, cacheEvent{kind: evMiss, key: first, primary: first, section: section})
			tentative = -1
		case reMissPlain.MatchString(line):
			events = append(events, cacheEvent{kind: evMiss, key: primary, section: section})
			tentative = -1
		case reSaved.MatchString(line):
			key := reSaved.FindStringSubmatch(line)[1]
			// A miss with an unknown key (setup-go "Cache is not found") is
			// named by the key its post step saves. Post steps run in
			// reverse setup order, so each save names the *latest* still-
			// unnamed miss — that pairing is exact even with several
			// setup-* actions in one job.
			for i := len(events) - 1; i >= 0; i-- {
				if events[i].kind == evMiss && events[i].key == "" {
					events[i].key = key
					break
				}
			}
			events = append(events, cacheEvent{kind: evSave, key: key, section: section, sizeMB: pendingSize})
			pendingSize = 0
		case reConflict.MatchString(line):
			events = append(events, cacheEvent{kind: evConflict, section: section})
		}
	}
	return events
}

// classifyHit decides exact-vs-partial: a hit is partial when we know the
// requested primary key and the restored key differs (restore-keys match).
func classifyHit(key, primary, section string, sizeMB float64) cacheEvent {
	kind := evHit
	if primary != "" && key != primary {
		kind = evPartial
	}
	return cacheEvent{kind: kind, key: key, primary: primary, section: section, sizeMB: sizeMB}
}

// normalizeCacheKey collapses hashes/long numbers so per-commit keys group
// together: "Linux-go-4ae0e4f8…" -> "Linux-go-*".
func normalizeCacheKey(key string) string {
	key = reHex.ReplaceAllString(key, "*")
	key = reLongNum.ReplaceAllString(key, "*")
	for strings.Contains(key, "*-*") {
		key = strings.ReplaceAll(key, "*-*", "*")
	}
	for strings.Contains(key, "**") {
		key = strings.ReplaceAll(key, "**", "*")
	}
	return key
}

// groupLabel picks the reporting bucket for an event.
func groupLabel(ev cacheEvent) string {
	k := ev.primary
	if k == "" {
		k = ev.key
	}
	if k == "" {
		if ev.section != "" {
			return ev.section + " (built-in cache)"
		}
		return "(unknown)"
	}
	return normalizeCacheKey(k)
}

// pickLogSampleJobs selects up to n completed jobs to sample, newest first,
// round-robin across (workflow-agnostic) base job names so one chatty matrix
// doesn't crowd out every other job.
func pickLogSampleJobs(jobsByRun map[int64][]Job, n int) []Job {
	byName := map[string][]Job{}
	for _, jobs := range jobsByRun {
		for _, j := range jobs {
			if j.Status != "completed" || j.Conclusion == "skipped" || j.Conclusion == "cancelled" {
				continue
			}
			name := baseJobName(j.Name)
			byName[name] = append(byName[name], j)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		sort.Slice(byName[name], func(a, b int) bool {
			return byName[name][a].CreatedAt.After(byName[name][b].CreatedAt)
		})
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Job
	for round := 0; len(out) < n; round++ {
		added := false
		for _, name := range names {
			if round < len(byName[name]) && len(out) < n {
				out = append(out, byName[name][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return out
}

// jobCacheSample is one sampled job's cache activity, timestamped for the
// trend split.
type jobCacheSample struct {
	t        time.Time
	restores int
	effHits  int // hits + partial hits
}

const (
	trendMinRestores = 10             // per half; below this the split is noise
	trendMinSpan     = 24 * time.Hour // whole sample from one afternoon is not a trend
)

// computeCacheTrend splits the sampled jobs that had cache activity into an
// older and a newer half (by job creation time) and compares hit rates.
// Returns nil when the sample is too small or too compressed in time to
// support a trend claim — better no trend line than a made-up one.
func computeCacheTrend(samples []jobCacheSample) *CacheTrend {
	var active []jobCacheSample
	for _, s := range samples {
		if s.restores > 0 {
			active = append(active, s)
		}
	}
	if len(active) < 4 {
		return nil
	}
	sort.Slice(active, func(a, b int) bool { return active[a].t.Before(active[b].t) })
	if active[len(active)-1].t.Sub(active[0].t) < trendMinSpan {
		return nil
	}
	mid := len(active) / 2
	older, newer := active[:mid], active[mid:]
	sum := func(half []jobCacheSample) (restores, effHits int) {
		for _, s := range half {
			restores += s.restores
			effHits += s.effHits
		}
		return
	}
	or, oh := sum(older)
	nr, nh := sum(newer)
	if or < trendMinRestores || nr < trendMinRestores {
		return nil
	}
	tr := &CacheTrend{
		OlderFrom: older[0].t, OlderTo: older[len(older)-1].t,
		NewerFrom: newer[0].t, NewerTo: newer[len(newer)-1].t,
		OlderRestores: or, NewerRestores: nr,
		OlderHitRate: float64(oh) / float64(or) * 100,
		NewerHitRate: float64(nh) / float64(nr) * 100,
	}
	tr.DeltaPts = tr.NewerHitRate - tr.OlderHitRate
	return tr
}

// analyzeCacheLogs samples job logs and aggregates cache hit/miss stats.
func (c *Client) analyzeCacheLogs(owner, repo string, jobsByRun map[int64][]Job, sample int, progress func(string)) *CacheLogStats {
	st := &CacheLogStats{}
	if c.Token == "" {
		st.Note = "cache hit-rate needs auth (job-log downloads 403 without a token, even on public repos); set GITHUB_TOKEN or run `gh auth login`"
		return st
	}
	jobs := pickLogSampleJobs(jobsByRun, sample)
	if len(jobs) == 0 {
		st.Note = "no completed jobs to sample"
		return st
	}
	progress(fmt.Sprintf("sampling %d job logs for cache hit/miss markers…", len(jobs)))

	type result struct {
		events  []cacheEvent
		skipped bool
	}
	results := make([]result, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var abort error
	var mu sync.Mutex
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, jobID int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			stop := abort != nil
			mu.Unlock()
			if stop {
				results[i].skipped = true
				return
			}
			text, err := c.GetJobLogs(owner, repo, jobID)
			if err != nil {
				var rle *RateLimitError
				if errors.As(err, &rle) {
					mu.Lock()
					abort = err
					mu.Unlock()
				}
				results[i].skipped = true
				return
			}
			results[i].events = parseCacheLog(text)
		}(i, j.ID)
	}
	wg.Wait()

	type agg struct {
		restores, hits, partial, misses int
		sizeSum                         float64
		sized                           int
	}
	groups := map[string]*agg{}
	samples := make([]jobCacheSample, 0, len(results))
	for i, r := range results {
		if r.skipped {
			st.JobsSkipped++
			continue
		}
		st.JobsSampled++
		js := jobCacheSample{t: jobs[i].CreatedAt}
		for _, ev := range r.events {
			switch ev.kind {
			case evSave:
				st.Saves++
				continue
			case evConflict:
				st.SaveConflicts++
				continue
			}
			label := groupLabel(ev)
			g := groups[label]
			if g == nil {
				g = &agg{}
				groups[label] = g
			}
			g.restores++
			st.Restores++
			js.restores++
			switch ev.kind {
			case evHit:
				g.hits++
				st.Hits++
				js.effHits++
			case evPartial:
				g.partial++
				st.PartialHits++
				js.effHits++
			case evMiss:
				g.misses++
				st.Misses++
			}
			if ev.sizeMB > 0 {
				st.RestoredMB += ev.sizeMB
				g.sizeSum += ev.sizeMB
				g.sized++
			}
		}
		samples = append(samples, js)
	}
	st.Trend = computeCacheTrend(samples)
	if abort != nil {
		st.Note = abort.Error()
	}
	if st.Restores > 0 {
		st.Available = true
		st.HitRate = float64(st.Hits+st.PartialHits) / float64(st.Restores) * 100
	} else if st.Note == "" {
		st.Note = fmt.Sprintf("no cache activity found in %d sampled job logs", st.JobsSampled)
	}
	for label, g := range groups {
		kg := CacheKeyGroup{
			Pattern: label, Restores: g.restores, Hits: g.hits,
			Partial: g.partial, Misses: g.misses,
			HitPct: float64(g.hits+g.partial) / float64(g.restores) * 100,
		}
		if g.sized > 0 {
			kg.AvgMB = g.sizeSum / float64(g.sized)
		}
		st.Groups = append(st.Groups, kg)
	}
	sort.Slice(st.Groups, func(a, b int) bool {
		if st.Groups[a].Restores != st.Groups[b].Restores {
			return st.Groups[a].Restores > st.Groups[b].Restores
		}
		return st.Groups[a].Pattern < st.Groups[b].Pattern
	})
	return st
}
