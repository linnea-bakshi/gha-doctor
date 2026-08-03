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
	case strings.Contains(n, "junit") || strings.Contains(n, "surefire"):
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

// attachArtifactTests scans the run's uploaded artifacts for JUnit XML
// test reports and records failing tests at run level. Called only when
// the failed jobs' logs named nothing — when console output already told
// the story, the extra downloads buy nothing.
func (c *Client) attachArtifactTests(owner, repo string, d *RunDeep, progress func(string)) {
	arts, err := c.ListRunArtifacts(owner, repo, d.RunID)
	if err != nil {
		progress(fmt.Sprintf("  artifact listing unavailable: %v", err))
		return
	}
	if len(arts) == 0 {
		return
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
	if len(cands) == 0 {
		if expiredCands > 0 {
			d.ArtifactTestNote = "the run's test-report artifacts have expired — no JUnit XML left to read"
		}
		return
	}
	// Likeliest names first; smaller uploads first within a rank (a shard's
	// junit.xml is kilobytes — the multi-hundred-MB upload is a bundle).
	sort.SliceStable(cands, func(i, k int) bool {
		si, sk := junitArtifactScore(cands[i].Name), junitArtifactScore(cands[k].Name)
		if si != sk {
			return si > sk
		}
		return cands[i].SizeInBytes < cands[k].SizeInBytes
	})
	if len(cands) > maxRunArtifacts {
		cands = cands[:maxRunArtifacts]
	}
	seen := map[string]bool{}
	scanned, reports, cases := 0, 0, 0
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
		scanned++
		names, n, files := scanJUnitZip(data)
		reports += files
		cases += n
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if len(d.ArtifactTests) == maxRunFailedTests {
				d.ArtifactTestsMore++
				continue
			}
			d.ArtifactTests = append(d.ArtifactTests, ArtifactFailedTest{Name: name, Artifact: a.Name})
		}
	}
	switch {
	case len(d.ArtifactTests) > 0:
		// The section speaks for itself.
	case scanned > 0 && reports > 0:
		d.ArtifactTestNote = fmt.Sprintf("JUnit test reports in the run's artifacts record %d test cases and no failures — the failure likely happened outside the reported tests (or the failing shard uploaded no report)", cases)
	case scanned > 0:
		d.ArtifactTestNote = fmt.Sprintf("no JUnit XML test reports found in %d scanned artifact(s)", scanned)
	}
}
