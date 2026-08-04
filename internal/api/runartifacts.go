package api

// Run-scoped artifact handling for --run deep dives: when a failed job's
// log names no tests, the run's uploaded JUnit XML test reports (if any)
// are the next-best exact-name source. See junit.go for the parser and the
// honesty constraints (run-scoped attribution, absence-of-failures caveat).

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// How many candidate artifacts get downloaded per deep dive (one request
// each, plus the redirect fetch) and how big a zip we accept. A red matrix
// can upload dozens of per-shard artifacts; the first few carry the story.
const (
	maxRunArtifacts     = 4
	maxArtifactZipBytes = 30 << 20
)

// ArtifactFailedTest is one failing test recorded in a JUnit XML test
// report uploaded as a run artifact. Artifact names the upload it came
// from — attribution stops there: artifacts belong to the run, not to a
// specific job.
type ArtifactFailedTest struct {
	Name     string `json:"name"`
	Artifact string `json:"artifact"`
}

// ListRunArtifacts fetches the artifacts uploaded by one workflow run
// (first 100 — more than any candidate cap we apply).
func (c *Client) ListRunArtifacts(owner, repo string, runID int64) ([]Artifact, error) {
	var resp struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	params := url.Values{"per_page": {"100"}}
	if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/runs/%d/artifacts", owner, repo, runID), params, &resp); err != nil {
		return nil, err
	}
	return resp.Artifacts, nil
}

// DownloadArtifactZip fetches an artifact's zip archive. GitHub answers
// with a redirect to blob storage, which net/http follows (dropping the
// auth header cross-domain, as the signed URL requires). Requires a token:
// this endpoint 403s unauthenticated even on public repos. Reads at most
// maxBytes and errors beyond it — a truncated zip has no usable central
// directory, so a partial read would only pretend to work.
func (c *Client) DownloadArtifactZip(owner, repo string, id int64, maxBytes int64) ([]byte, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/actions/artifacts/%d/zip", c.BaseURL, owner, repo, id)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return nil, &RateLimitError{Message: "GitHub API rate limit exceeded"}
	}
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return nil, &NotFoundError{Path: fmt.Sprintf("/repos/%s/%s/actions/artifacts/%d/zip", owner, repo, id)}
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET artifact %d zip: %s", id, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("artifact %d zip exceeds %d MiB cap", id, maxBytes>>20)
	}
	return body, nil
}

// junitArtifactScore ranks an artifact name's likelihood of holding test
// reports: 0 = not a candidate.
func junitArtifactScore(name string) int {
	n := strings.ToLower(name)
	// Uploads that match the include words but are known to hold other
	// payloads (coverage XML is Cobertura/JaCoCo shaped, screenshots and
	// videos are bulky, playwright-report is HTML).
	for _, ex := range []string{"coverage", "screenshot", "video", "trace", "playwright-report"} {
		if strings.Contains(n, ex) {
			return 0
		}
	}
	switch {
	case strings.Contains(n, "junit") || strings.Contains(n, "surefire") ||
		strings.Contains(n, "trx") || strings.Contains(n, "nunit") ||
		strings.Contains(n, "testng"):
		return 3
	case strings.Contains(n, "test-result") || strings.Contains(n, "test-report") ||
		strings.Contains(n, "test_result") || strings.Contains(n, "test_report") ||
		strings.Contains(n, "testresult") || strings.Contains(n, "testreport"):
		return 2
	case strings.Contains(n, "test") && (strings.Contains(n, "result") || strings.Contains(n, "report")):
		return 2
	case strings.Contains(n, "test"):
		return 1
	}
	return 0
}

// artifactTokens splits a name into lowercase alphanumeric tokens, dropping
// single characters and words too generic to signal a relationship between
// an artifact and a job ("test-results-bs_safari" → bs, safari).
func artifactTokens(name string) map[string]bool {
	generic := map[string]bool{
		"test": true, "tests": true, "result": true, "results": true,
		"report": true, "reports": true, "run": true, "job": true,
		"build": true, "ci": true, "latest": true, "on": true, "of": true,
	}
	out := map[string]bool{}
	for _, t := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9')
	}) {
		if len(t) >= 2 && !generic[t] {
			out[t] = true
		}
	}
	return out
}

// artifactJobAffinity counts how many distinctive tokens an artifact name
// shares with the best-matching failed job — used to rank the failing
// shard's own upload ("test-results-bs_safari" for job "Test (bs_safari)")
// ahead of its green siblings within the same name-based rank, so the
// download cap can't drop the one report that matters.
func artifactJobAffinity(name string, failedJobTokens []map[string]bool) int {
	at := artifactTokens(name)
	best := 0
	for _, jt := range failedJobTokens {
		n := 0
		for t := range jt {
			if at[t] {
				n++
			}
		}
		if n > best {
			best = n
		}
	}
	return best
}

// artifactScan is the outcome of scanning one run's artifacts for JUnit
// XML test reports.
type artifactScan struct {
	Tests       []ArtifactFailedTest
	More        int  // failing tests beyond maxTests
	Scanned     int  // zips downloaded and parsed
	Reports     int  // test-report files seen (any recognized format)
	Cases       int  // test cases those reports record
	Candidates  int  // non-expired candidate artifacts (before the cap)
	ExpiredOnly bool // candidates existed but all had expired
	Truncated   bool // a parse budget left candidate report files unread
}

// scanRunArtifactsForTests lists one run's artifacts, ranks the likely
// test-report uploads (name score, then shared-token affinity with a
// failed job so the failing shard's own upload survives the cap, then
// smallest first) and parses up to maxDownloads of them for failing-test
// names. Attribution stops at the run: artifacts belong to the run, not
// to a specific job.
func (c *Client) scanRunArtifactsForTests(owner, repo string, runID int64, failedJobs []Job, maxDownloads, maxTests int, progress func(string)) (artifactScan, error) {
	var out artifactScan
	arts, err := c.ListRunArtifacts(owner, repo, runID)
	if err != nil {
		return out, err
	}
	var cands []Artifact
	expiredCands := 0
	for _, a := range arts {
		if junitArtifactScore(a.Name) == 0 {
			continue
		}
		if a.Expired {
			expiredCands++
			continue
		}
		cands = append(cands, a)
	}
	out.Candidates = len(cands)
	if len(cands) == 0 {
		out.ExpiredOnly = expiredCands > 0
		return out, nil
	}
	// Likeliest names first; within a rank, artifacts sharing name tokens
	// with a failed job come first (the failing shard's own upload must
	// survive the download cap); then smaller uploads first (a shard's
	// junit.xml is kilobytes — the multi-hundred-MB upload is a bundle).
	var failedJobTokens []map[string]bool
	for _, j := range failedJobs {
		if j.Conclusion == "failure" || j.Conclusion == "timed_out" {
			failedJobTokens = append(failedJobTokens, artifactTokens(j.Name))
		}
	}
	affinity := make([]int, len(cands))
	for i, a := range cands {
		affinity[i] = artifactJobAffinity(a.Name, failedJobTokens)
	}
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, k int) bool {
		ci, ck := order[i], order[k]
		si, sk := junitArtifactScore(cands[ci].Name), junitArtifactScore(cands[ck].Name)
		if si != sk {
			return si > sk
		}
		if affinity[ci] != affinity[ck] {
			return affinity[ci] > affinity[ck]
		}
		return cands[ci].SizeInBytes < cands[ck].SizeInBytes
	})
	sorted := make([]Artifact, len(cands))
	for i, ci := range order {
		sorted[i] = cands[ci]
	}
	cands = sorted
	if len(cands) > maxDownloads {
		cands = cands[:maxDownloads]
	}
	seen := map[string]bool{}
	for _, a := range cands {
		if a.SizeInBytes > maxArtifactZipBytes {
			continue
		}
		progress(fmt.Sprintf("checking artifact %q for test reports…", a.Name))
		data, err := c.DownloadArtifactZip(owner, repo, a.ID, maxArtifactZipBytes)
		if err != nil {
			progress(fmt.Sprintf("  artifact unavailable: %v", err))
			continue
		}
		out.Scanned++
		names, n, files, trunc := scanJUnitZip(data)
		out.Reports += files
		out.Cases += n
		if trunc {
			out.Truncated = true
		}
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if len(out.Tests) == maxTests {
				out.More++
				continue
			}
			out.Tests = append(out.Tests, ArtifactFailedTest{Name: name, Artifact: a.Name})
		}
	}
	return out, nil
}

// attachArtifactTests scans the run's uploaded artifacts for JUnit XML
// test reports and records failing tests at run level. Called only when
// the failed jobs' logs named nothing — when console output already told
// the story, the extra downloads buy nothing.
func (c *Client) attachArtifactTests(owner, repo string, d *RunDeep, progress func(string)) {
	jobs := make([]Job, 0, len(d.Jobs))
	for _, j := range d.Jobs {
		jobs = append(jobs, Job{Name: j.Name, Conclusion: j.Conclusion})
	}
	sc, err := c.scanRunArtifactsForTests(owner, repo, d.RunID, jobs, maxRunArtifacts, maxRunFailedTests, progress)
	if err != nil {
		progress(fmt.Sprintf("  artifact listing unavailable: %v", err))
		return
	}
	d.ArtifactTests = sc.Tests
	d.ArtifactTestsMore = sc.More
	switch {
	case len(d.ArtifactTests) > 0:
		// The section speaks for itself — unless the scan was cut short,
		// in which case the list must not pose as complete.
	case sc.ExpiredOnly:
		d.ArtifactTestNote = "the run's test-report artifacts have expired — no JUnit XML, TRX, NUnit3 or TestNG reports left to read"
	case sc.Scanned > 0 && sc.Reports > 0:
		d.ArtifactTestNote = fmt.Sprintf("test reports (JUnit XML/TRX/NUnit3/TestNG) in the run's artifacts record %d test cases and no failures — the failure likely happened outside the reported tests (or the failing shard uploaded no report)", sc.Cases)
	case sc.Scanned > 0:
		d.ArtifactTestNote = fmt.Sprintf("no JUnit XML, TRX, NUnit3 or TestNG test reports found in %d scanned artifact(s)", sc.Scanned)
	}
	if sc.Truncated {
		note := "the per-artifact parse budget left some report files unread — the failing-test list may be incomplete"
		if d.ArtifactTestNote != "" {
			d.ArtifactTestNote += "; " + note
		} else {
			d.ArtifactTestNote = note
		}
	}
}
