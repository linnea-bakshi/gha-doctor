package api

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Number of recent successful runs of the same workflow used as the
// comparison baseline for a --run deep dive. Each costs one jobs request;
// keeping it small leaves room in unauthenticated rate limits (60/h).
const runDeepBaseline = 8

// Fewer comparable runs than this and per-step/wall-clock comparisons are
// noise, so they are dropped rather than reported.
const runDeepMinBaseline = 3

// RunDeep is the deep dive into a single workflow run: what ran when, and
// how each part compares to the workflow's own recent history.
type RunDeep struct {
	Repo        string    `json:"repo"`
	RunID       int64     `json:"run_id"`
	RunNumber   int       `json:"run_number"`
	Workflow    string    `json:"workflow"`
	Title       string    `json:"title,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	Event       string    `json:"event,omitempty"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion,omitempty"`
	URL         string    `json:"url,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	Attempt     int       `json:"attempt"`                // run attempt number (re-runs bump this)
	WallSec     float64   `json:"wall_sec"`               // run start → last job completion
	InProgress  bool      `json:"in_progress"`            // wall clock is "so far", not final
	RetriedJobs int       `json:"retried_jobs"`           // jobs that executed again in this attempt
	CarriedJobs int       `json:"carried_jobs,omitempty"` // finished in an earlier attempt, not re-run
	Jobs        []DeepJob `json:"jobs"`

	// Baseline: recent successful runs of the same workflow.
	BaselineRuns    int     `json:"baseline_runs"`
	BaselineWallP50 float64 `json:"baseline_wall_p50_sec,omitempty"`
	BaselineNote    string  `json:"baseline_note,omitempty"`

	// LogNote explains why failing-step log tails are absent (no token).
	LogNote string `json:"log_note,omitempty"`
}

// DeepJob is one job in the run (latest attempt), positioned on the run's
// wall-clock timeline.
type DeepJob struct {
	Name       string     `json:"name"`
	Conclusion string     `json:"conclusion"`
	StartSec   float64    `json:"start_sec"` // offset from run start
	EndSec     float64    `json:"end_sec"`
	QueueSec   float64    `json:"queue_sec"` // created → started (runner wait)
	DurSec     float64    `json:"dur_sec"`
	Attempts   int        `json:"attempts,omitempty"` // >1 when earlier attempts exist
	BaselineN  int        `json:"baseline_n,omitempty"`
	P50Sec     float64    `json:"baseline_p50_sec,omitempty"`
	Steps      []DeepStep `json:"steps,omitempty"`

	// Failure log tail: last lines of the failing step's log (needs a token).
	LogStep string   `json:"log_step,omitempty"`
	LogTail []string `json:"log_tail,omitempty"`
}

// DeepStep is one step within a job, with its historical median when the
// baseline has enough samples of the same step in the same job.
type DeepStep struct {
	Name       string  `json:"name"`
	Number     int     `json:"number"`
	Conclusion string  `json:"conclusion"`
	DurSec     float64 `json:"dur_sec"`
	BaselineN  int     `json:"baseline_n,omitempty"`
	P50Sec     float64 `json:"baseline_p50_sec,omitempty"`
}

// GetRun fetches a single workflow run by ID.
func (c *Client) GetRun(owner, repo string, id int64) (*Run, error) {
	var r Run
	if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/runs/%d", owner, repo, id), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// LatestRun returns the most recent run for the repo (any status), so
// `--run latest` can inspect a run that is still in progress.
func (c *Client) LatestRun(owner, repo string) (*Run, error) {
	var resp struct {
		WorkflowRuns []Run `json:"workflow_runs"`
	}
	params := url.Values{"per_page": {"1"}}
	if err := c.get(fmt.Sprintf("/repos/%s/%s/actions/runs", owner, repo), params, &resp); err != nil {
		return nil, err
	}
	if len(resp.WorkflowRuns) == 0 {
		return nil, fmt.Errorf("no workflow runs found for %s/%s", owner, repo)
	}
	return &resp.WorkflowRuns[0], nil
}

// listWorkflowRuns fetches recent successful runs of one workflow.
func (c *Client) listWorkflowRuns(owner, repo string, workflowID int64, max int) ([]Run, error) {
	var resp struct {
		WorkflowRuns []Run `json:"workflow_runs"`
	}
	params := url.Values{
		"per_page": {fmt.Sprint(max)},
		"status":   {"success"},
	}
	err := c.get(fmt.Sprintf("/repos/%s/%s/actions/workflows/%d/runs", owner, repo, workflowID), params, &resp)
	return resp.WorkflowRuns, err
}

// AnalyzeRun builds the deep dive for one run: timeline from its jobs plus
// per-job/per-step medians from recent successful runs of the same workflow.
// logTail is how many trailing log lines to attach for failed jobs (0 = off).
func (c *Client) AnalyzeRun(owner, repo string, run *Run, logTail int, progress func(string)) (*RunDeep, error) {
	if progress == nil {
		progress = func(string) {}
	}
	d := &RunDeep{
		Repo:       owner + "/" + repo,
		RunID:      run.ID,
		RunNumber:  run.RunNumber,
		Workflow:   run.Name,
		Title:      run.DisplayTitle,
		Branch:     run.HeadBranch,
		Event:      run.Event,
		Status:     run.Status,
		Conclusion: run.Conclusion,
		URL:        run.HTMLURL,
		StartedAt:  run.RunStartedAt,
		Attempt:    run.RunAttempt,
		InProgress: run.Status != "completed",
	}

	progress(fmt.Sprintf("fetching jobs for run %d…", run.ID))
	jobs, err := c.ListJobs(owner, repo, run.ID)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("run %d has no jobs", run.ID)
	}
	latest, retried := latestAttempts(jobs)

	// Baseline: recent successful runs of the same workflow (excluding this
	// run), medians per job name and per (job, step) name.
	progress(fmt.Sprintf("fetching %d recent successful %q runs for comparison…", runDeepBaseline, run.Name))
	baseRuns, err := c.listWorkflowRuns(owner, repo, run.WorkflowID, runDeepBaseline+1)
	if err != nil {
		progress("baseline unavailable: " + err.Error())
		baseRuns = nil
	}
	jobDurs := map[string][]float64{}     // job name → durations
	stepDurs := map[[2]string][]float64{} // job|step → durations
	wallDurs := []float64{}
	used := 0
	for _, br := range baseRuns {
		if br.ID == run.ID || used == runDeepBaseline {
			continue
		}
		bjobs, err := c.ListJobs(owner, repo, br.ID)
		if err != nil {
			continue
		}
		blatest, _ := latestAttempts(bjobs)
		var lastEnd time.Time
		for _, j := range blatest {
			if j.Status != "completed" || j.CompletedAt.IsZero() {
				continue
			}
			jobDurs[j.Name] = append(jobDurs[j.Name], j.CompletedAt.Sub(j.StartedAt).Seconds())
			if j.CompletedAt.After(lastEnd) {
				lastEnd = j.CompletedAt
			}
			for _, st := range j.Steps {
				if st.CompletedAt.IsZero() || st.StartedAt.IsZero() || st.Conclusion == "skipped" {
					continue
				}
				stepDurs[[2]string{j.Name, st.Name}] = append(stepDurs[[2]string{j.Name, st.Name}], st.CompletedAt.Sub(st.StartedAt).Seconds())
			}
		}
		if !lastEnd.IsZero() && lastEnd.After(br.RunStartedAt) {
			wallDurs = append(wallDurs, lastEnd.Sub(br.RunStartedAt).Seconds())
		}
		used++
		total := len(baseRuns)
		if total > runDeepBaseline {
			total = runDeepBaseline
		}
		progress(fmt.Sprintf("  baseline %d/%d…", used, total))
	}
	d.BaselineRuns = used
	if used >= runDeepMinBaseline {
		sort.Float64s(wallDurs)
		d.BaselineWallP50 = percentile(wallDurs, 0.5)
	} else if used > 0 {
		d.BaselineNote = fmt.Sprintf("only %d comparable successful runs of this workflow — too few to compare against", used)
	} else {
		d.BaselineNote = "no other successful runs of this workflow to compare against"
	}

	// Timeline for the target run. On a re-run (attempt > 1) the run's
	// clock restarts, so only jobs that executed in this attempt belong on
	// the timeline; jobs whose result was carried over from an earlier
	// attempt (re-run failed jobs leaves successes alone) are counted, not
	// drawn — their timestamps predate this attempt's start.
	var lastEnd time.Time
	for _, j := range latest {
		if run.RunAttempt > 1 && j.RunAttempt < run.RunAttempt {
			d.CarriedJobs++
			continue
		}
		if retried[j.Name] > 0 {
			d.RetriedJobs++
		}
		dj := DeepJob{
			Name:       j.Name,
			Conclusion: j.Conclusion,
			Attempts:   retried[j.Name] + 1,
		}
		if dj.Conclusion == "" {
			dj.Conclusion = j.Status // queued / in_progress
		}
		if !j.StartedAt.IsZero() {
			dj.StartSec = clampNonNeg(j.StartedAt.Sub(run.RunStartedAt).Seconds())
			if !j.CreatedAt.IsZero() && j.StartedAt.After(j.CreatedAt) {
				dj.QueueSec = j.StartedAt.Sub(j.CreatedAt).Seconds()
			}
		}
		if !j.CompletedAt.IsZero() && !j.StartedAt.IsZero() {
			dj.DurSec = j.CompletedAt.Sub(j.StartedAt).Seconds()
			dj.EndSec = dj.StartSec + dj.DurSec
			if j.CompletedAt.After(lastEnd) {
				lastEnd = j.CompletedAt
			}
		}
		if used >= runDeepMinBaseline {
			if ds := jobDurs[j.Name]; len(ds) >= runDeepMinBaseline {
				sort.Float64s(ds)
				dj.BaselineN = len(ds)
				dj.P50Sec = percentile(ds, 0.5)
			}
		}
		for _, st := range j.Steps {
			if st.StartedAt.IsZero() || st.CompletedAt.IsZero() || st.Conclusion == "skipped" {
				continue
			}
			dstep := DeepStep{
				Name:       st.Name,
				Number:     st.Number,
				Conclusion: st.Conclusion,
				DurSec:     st.CompletedAt.Sub(st.StartedAt).Seconds(),
			}
			if used >= runDeepMinBaseline {
				if ds := stepDurs[[2]string{j.Name, st.Name}]; len(ds) >= runDeepMinBaseline {
					sort.Float64s(ds)
					dstep.BaselineN = len(ds)
					dstep.P50Sec = percentile(ds, 0.5)
				}
			}
			dj.Steps = append(dj.Steps, dstep)
		}
		d.Jobs = append(d.Jobs, dj)
	}
	sort.Slice(d.Jobs, func(i, k int) bool {
		if d.Jobs[i].StartSec != d.Jobs[k].StartSec {
			return d.Jobs[i].StartSec < d.Jobs[k].StartSec
		}
		return d.Jobs[i].Name < d.Jobs[k].Name
	})
	if !lastEnd.IsZero() && lastEnd.After(run.RunStartedAt) {
		d.WallSec = lastEnd.Sub(run.RunStartedAt).Seconds()
	}
	if logTail > 0 {
		byName := make(map[string]Job, len(latest))
		for _, j := range latest {
			byName[j.Name] = j
		}
		c.attachFailLogs(owner, repo, d, byName, logTail, progress)
	}
	return d, nil
}

// latestAttempts keeps only the newest attempt of each job name and counts
// how many earlier-attempt executions each name had.
func latestAttempts(jobs []Job) ([]Job, map[string]int) {
	best := map[string]Job{}
	earlier := map[string]int{}
	for _, j := range jobs {
		cur, ok := best[j.Name]
		if !ok {
			best[j.Name] = j
			continue
		}
		earlier[j.Name]++
		if j.RunAttempt > cur.RunAttempt {
			best[j.Name] = j
		}
	}
	out := make([]Job, 0, len(best))
	for _, j := range best {
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].ID < out[k].ID })
	return out, earlier
}

func clampNonNeg(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// ParseRunID parses the --run flag value: a numeric run ID or "latest".
func ParseRunID(v string) (int64, bool, error) {
	if strings.EqualFold(v, "latest") {
		return 0, true, nil
	}
	// Accept a pasted run URL tail like "…/actions/runs/123456".
	if i := strings.LastIndex(v, "/"); i >= 0 {
		v = v[i+1:]
	}
	var id int64
	if _, err := fmt.Sscanf(v, "%d", &id); err != nil || id <= 0 {
		return 0, false, fmt.Errorf("--run wants a run ID (or 'latest'), got %q", v)
	}
	return id, false, nil
}
