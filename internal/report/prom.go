package report

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// Prom writes the report's aggregates in the Prometheus text exposition
// format, for a node_exporter textfile collector or any Prometheus-
// compatible scraper. Run gha-doctor on a schedule, drop the file where the
// collector looks, and CI health becomes a dashboard: score, success rates,
// wasted compute, flaky jobs, cache health, PR feedback time — over time.
//
// Honesty rules, same as --json's omitempty:
//   - a section the run did not measure emits NO series at all — a gap on
//     the dashboard is the truth; a zero-filled series would be a lie.
//   - a measured zero (e.g. zero flaky jobs across a sampled window) IS
//     emitted: "we looked, it's zero" is information.
//   - every figure describes the sampled window, not all time, so
//     everything is a gauge; gha_doctor_sample_since_timestamp_seconds
//     says how far back the sample reaches.
//
// Cardinality: per-workflow series are labeled with the workflow name —
// bounded by the repo's workflow count. Per-job, per-test and per-cache
// breakdowns deliberately stay out; they belong in the report, not a TSDB.
func Prom(w io.Writer, version, repo string, findings []lint.Finding, filesScanned int, a *api.Analysis, sc *Score, now time.Time) error {
	p := &promWriter{w: w, repo: repo}

	p.family("gha_doctor_info", "Build info for the gha-doctor binary that produced this file.")
	p.sample("gha_doctor_info", kv{"version", version}, 1)

	p.family("gha_doctor_last_run_timestamp_seconds", "Unix time this file was generated; alert on staleness.")
	p.sample("gha_doctor_last_run_timestamp_seconds", kv{}, float64(now.Unix()))

	if filesScanned > 0 || len(findings) > 0 {
		p.family("gha_doctor_files_scanned", "Workflow and action-manifest files covered by the static checks.")
		p.sample("gha_doctor_files_scanned", kv{}, float64(filesScanned))

		warns, infos := 0, 0
		for _, f := range findings {
			if f.Severity == lint.Warn {
				warns++
			} else {
				infos++
			}
		}
		p.family("gha_doctor_findings", "Current static findings by severity.")
		p.sample("gha_doctor_findings", kv{"severity", "warning"}, float64(warns))
		p.sample("gha_doctor_findings", kv{"severity", "info"}, float64(infos))
	}

	if sc != nil && len(sc.Components) > 0 {
		p.family("gha_doctor_health_score_points", "Health score, 0-100 ("+sc.Basis+").")
		p.sample("gha_doctor_health_score_points", kv{}, float64(sc.Points))
	}

	if a != nil {
		p.analysis(a)
	}
	return p.err
}

// kv is a single label pair; promWriter.sample prepends the repo label.
type kv [2]string

type promWriter struct {
	w    io.Writer
	repo string
	err  error
}

func (p *promWriter) family(name, help string) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, "# HELP %s %s\n# TYPE %s gauge\n", name, promEscapeHelp(help), name)
}

func (p *promWriter) sample(name string, extra kv, v float64) {
	if p.err != nil {
		return
	}
	var labels []string
	if p.repo != "" {
		labels = append(labels, `repo="`+promEscapeLabel(p.repo)+`"`)
	}
	if extra[0] != "" {
		labels = append(labels, extra[0]+`="`+promEscapeLabel(extra[1])+`"`)
	}
	lbl := ""
	if len(labels) > 0 {
		lbl = "{" + strings.Join(labels, ",") + "}"
	}
	_, p.err = fmt.Fprintf(p.w, "%s%s %s\n", name, lbl, promValue(v))
}

func (p *promWriter) analysis(a *api.Analysis) {
	p.family("gha_doctor_runs_sampled", "Workflow runs in the analysis sample.")
	p.sample("gha_doctor_runs_sampled", kv{}, float64(a.RunsSampled))

	p.family("gha_doctor_sample_since_timestamp_seconds", "Unix time of the oldest sampled run; every gauge below describes the window from then to now.")
	p.sample("gha_doctor_sample_since_timestamp_seconds", kv{}, float64(a.Since.Unix()))

	p.family("gha_doctor_runs_missing_job_data", "Sampled runs whose per-job data could not be fetched; when >0, job-derived gauges (queue, cost, waste, flakiness) understate.")
	p.sample("gha_doctor_runs_missing_job_data", kv{}, float64(a.JobDataMissing))

	// Per-workflow series, sorted for stable output.
	wfs := append([]api.WorkflowStats(nil), a.Workflows...)
	sort.Slice(wfs, func(i, j int) bool { return wfs[i].Name < wfs[j].Name })
	p.family("gha_doctor_workflow_runs", "Sampled runs per workflow.")
	for _, wf := range wfs {
		p.sample("gha_doctor_workflow_runs", kv{"workflow", wf.Name}, float64(wf.Runs))
	}
	p.family("gha_doctor_workflow_decisive_runs", "Sampled runs that reached a verdict (success or failure); skipped and cancelled runs are not decisive.")
	for _, wf := range wfs {
		p.sample("gha_doctor_workflow_decisive_runs", kv{"workflow", wf.Name}, float64(wf.Decisive))
	}
	p.family("gha_doctor_workflow_success_ratio", "Successful share of decisive runs, 0-1; absent when the workflow had no decisive run in the sample.")
	for _, wf := range wfs {
		if wf.Decisive > 0 {
			p.sample("gha_doctor_workflow_success_ratio", kv{"workflow", wf.Name}, wf.SuccessRate)
		}
	}
	p.family("gha_doctor_workflow_duration_p50_seconds", "Median wall-clock duration of decisive runs; absent when the workflow had no decisive run.")
	for _, wf := range wfs {
		if wf.Decisive > 0 {
			p.sample("gha_doctor_workflow_duration_p50_seconds", kv{"workflow", wf.Name}, wf.P50Minutes*60)
		}
	}
	p.family("gha_doctor_workflow_duration_p95_seconds", "95th-percentile wall-clock duration of decisive runs; absent when the workflow had no decisive run.")
	for _, wf := range wfs {
		if wf.Decisive > 0 {
			p.sample("gha_doctor_workflow_duration_p95_seconds", kv{"workflow", wf.Name}, wf.P95Minutes*60)
		}
	}
	p.family("gha_doctor_workflow_avg_queue_seconds", "Mean time a job of this workflow waited for a runner.")
	for _, wf := range wfs {
		p.sample("gha_doctor_workflow_avg_queue_seconds", kv{"workflow", wf.Name}, wf.AvgQueueSec)
	}
	p.family("gha_doctor_workflow_estimated_cost_usd", "Estimated compute value of this workflow's sampled runs at GitHub's hosted-runner rates; self-hosted jobs excluded.")
	for _, wf := range wfs {
		p.sample("gha_doctor_workflow_estimated_cost_usd", kv{"workflow", wf.Name}, wf.EstUSD)
	}

	p.family("gha_doctor_flaky_jobs", "Jobs that both failed and succeeded on the same commit within the sample.")
	p.sample("gha_doctor_flaky_jobs", kv{}, float64(len(a.FlakyJobs)))

	p.family("gha_doctor_zombie_crons", "Scheduled workflows whose newest sampled runs are an unbroken failure streak.")
	p.sample("gha_doctor_zombie_crons", kv{}, float64(len(a.ZombieCrons)))

	p.family("gha_doctor_waste_failed_run_seconds", "Billable-weighted compute spent in failed runs across the sample.")
	p.sample("gha_doctor_waste_failed_run_seconds", kv{}, a.Waste.FailedRunMinutes*60)
	p.family("gha_doctor_waste_retry_seconds", "Billable-weighted compute spent in earlier attempts that had to be re-run, across the sample.")
	p.sample("gha_doctor_waste_retry_seconds", kv{}, a.Waste.RetryMinutes*60)
	p.family("gha_doctor_compute_seconds", "All billable-weighted compute across the sample; the denominator for the waste gauges.")
	p.sample("gha_doctor_compute_seconds", kv{}, a.Waste.ComputeMinutes*60)

	p.family("gha_doctor_billable_seconds", "Billable compute across the sample: per-job ceil to the minute, OS-multiplier weighted.")
	p.sample("gha_doctor_billable_seconds", kv{}, a.Cost.BillableMinutes*60)
	p.family("gha_doctor_estimated_cost_usd", "Estimated compute value of the sample at GitHub's hosted-runner rates; self-hosted jobs excluded.")
	p.sample("gha_doctor_estimated_cost_usd", kv{}, a.Cost.EstimatedUSD)
	p.family("gha_doctor_wasted_cost_usd", "Share of the estimate spent in failed runs and retried attempts.")
	p.sample("gha_doctor_wasted_cost_usd", kv{}, a.Cost.WastedUSD)
	p.family("gha_doctor_rounding_seconds", "Billed-but-unused compute from the per-job round-up to whole minutes, weighted.")
	p.sample("gha_doctor_rounding_seconds", kv{}, a.Cost.RoundingMinutes*60)
	p.family("gha_doctor_rounding_cost_usd", "Value of the round-up compute.")
	p.sample("gha_doctor_rounding_cost_usd", kv{}, a.Cost.RoundingUSD)
	p.family("gha_doctor_self_hosted_jobs", "Sampled jobs on self-hosted runners, excluded from every cost estimate.")
	p.sample("gha_doctor_self_hosted_jobs", kv{}, float64(a.Cost.SelfHostedJobs))

	if a.Cache.Available {
		p.family("gha_doctor_cache_entries", "Actions cache entries currently held by the repo.")
		p.sample("gha_doctor_cache_entries", kv{}, float64(a.Cache.Count))
		p.family("gha_doctor_cache_size_bytes", "Total Actions cache storage currently held.")
		p.sample("gha_doctor_cache_size_bytes", kv{}, a.Cache.TotalMB*1024*1024)
		p.family("gha_doctor_cache_limit_ratio", "Cache storage as a share of the 10 GB per-repo soft limit; above 1.0 GitHub evicts.")
		p.sample("gha_doctor_cache_limit_ratio", kv{}, a.Cache.LimitPct/100)
		p.family("gha_doctor_cache_stale_bytes", "Cache storage not accessed in 7+ days (examined entries; see the report for sampling notes).")
		p.sample("gha_doctor_cache_stale_bytes", kv{}, a.Cache.StaleMB*1024*1024)
		p.family("gha_doctor_cache_pr_ref_bytes", "Cache storage held by refs/pull/* keys, unreachable from other branches (examined entries).")
		p.sample("gha_doctor_cache_pr_ref_bytes", kv{}, a.Cache.PRRefMB*1024*1024)
	}

	if cl := a.CacheLogs; cl != nil && cl.Available && cl.Restores > 0 {
		p.family("gha_doctor_cache_log_restores", "Cache restore attempts observed in the sampled job logs (--cache-logs).")
		p.sample("gha_doctor_cache_log_restores", kv{}, float64(cl.Restores))
		p.family("gha_doctor_cache_log_hit_ratio", "Share of observed restores that hit (exact or restore-keys), 0-1.")
		p.sample("gha_doctor_cache_log_hit_ratio", kv{}, cl.HitRate)
	}

	if a.Artifacts.Available {
		p.family("gha_doctor_artifact_entries", "Artifact entries the repo currently lists, including expired ones still indexed.")
		p.sample("gha_doctor_artifact_entries", kv{}, float64(a.Artifacts.Count))
	}

	if s := a.Superseded; s != nil {
		p.family("gha_doctor_superseded_completed_runs", "PR runs a newer push made obsolete that ran to completion anyway; what D001's cancel-in-progress prevents.")
		p.sample("gha_doctor_superseded_completed_runs", kv{}, float64(s.Completed))
		p.family("gha_doctor_superseded_cancelled_runs", "Superseded PR runs cancelled in time (concurrency at work).")
		p.sample("gha_doctor_superseded_cancelled_runs", kv{}, float64(s.Cancelled))
		p.family("gha_doctor_superseded_wasted_seconds", "Billable-weighted compute the completed superseded runs spent past their supersession moment.")
		p.sample("gha_doctor_superseded_wasted_seconds", kv{}, s.WastedMinutes*60)
		p.family("gha_doctor_superseded_wasted_cost_usd", "Value of that superseded compute.")
		p.sample("gha_doctor_superseded_wasted_cost_usd", kv{}, s.WastedUSD)
	}

	if f := a.Feedback; f != nil {
		p.family("gha_doctor_pr_feedback_pushes", "Qualifying PR pushes the feedback percentiles are computed from (full verdict arrived, no re-runs).")
		p.sample("gha_doctor_pr_feedback_pushes", kv{}, float64(f.Pushes))
		p.family("gha_doctor_pr_feedback_p50_seconds", "Median wait from a PR push to its last check finishing, queue time included.")
		p.sample("gha_doctor_pr_feedback_p50_seconds", kv{}, f.P50Minutes*60)
		p.family("gha_doctor_pr_feedback_p95_seconds", "95th-percentile wait from a PR push to its last check finishing.")
		p.sample("gha_doctor_pr_feedback_p95_seconds", kv{}, f.P95Minutes*60)
	}
}

// promValue renders a float without exponent notation or float noise:
// derived values (minutes × 60, percent ÷ 100) carry artifacts like
// 491.99999999999994 that no dashboard should ever display. Six decimals
// is far beyond the honest precision of anything measured here.
func promValue(v float64) string {
	return strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', -1, 64)
}

var promLabelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func promEscapeLabel(s string) string { return promLabelEscaper.Replace(s) }

var promHelpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

func promEscapeHelp(s string) string { return promHelpEscaper.Replace(s) }
