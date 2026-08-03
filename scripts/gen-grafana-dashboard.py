#!/usr/bin/env python3
"""Generate docs/grafana/gha-doctor-dashboard.json.

The committed dashboard is build output — edit THIS file and rerun:

    python3 scripts/gen-grafana-dashboard.py

CI regenerates and diffs it, so the two cannot drift.

Every panel queries only series gha-doctor's --prom mode actually emits
(see docs/honesty.md: unmeasured aggregates are ABSENT series, so panels
honestly show "No data" rather than fake zeros). Default time range is
24h: per-run snapshots accrue into trends; with very fresh data a longer
range can leave Grafana's step-aligned query points before the first
sample (see docs/grafana.md).
"""
import json
import os

DS = {"type": "prometheus", "uid": "${datasource}"}
_id = [0]


def nid():
    _id[0] += 1
    return _id[0]


def target(expr, legend=None, instant=False):
    t = {"expr": expr, "refId": "A", "datasource": DS}
    if legend:
        t["legendFormat"] = legend
    if instant:
        t["instant"] = True
        t["range"] = False
    return t


def targets(*ts):
    out = []
    for i, t in enumerate(ts):
        t = dict(t)
        t["refId"] = chr(ord("A") + i)
        out.append(t)
    return out


def base(ptype, title, x, y, w, h, desc=""):
    p = {
        "id": nid(),
        "type": ptype,
        "title": title,
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "datasource": DS,
    }
    if desc:
        p["description"] = desc
    return p


def thresholds(*steps):
    return {
        "mode": "absolute",
        "steps": [{"color": c, "value": v} for c, v in steps],
    }


def field(unit=None, ths=None, min_=None, max_=None, decimals=None, overrides=None):
    d = {"color": {"mode": "thresholds" if ths else "palette-classic"}}
    if unit is not None:
        d["unit"] = unit
    if ths is not None:
        d["thresholds"] = ths
    else:
        d["thresholds"] = thresholds(("green", None))
    if min_ is not None:
        d["min"] = min_
    if max_ is not None:
        d["max"] = max_
    if decimals is not None:
        d["decimals"] = decimals
    return {"defaults": d, "overrides": overrides or []}


def stat(title, x, y, w, h, expr, unit=None, ths=None, desc="", legend=None):
    p = base("stat", title, x, y, w, h, desc)
    p["targets"] = [target(expr, legend, instant=True)]
    p["fieldConfig"] = field(unit=unit, ths=ths)
    p["options"] = {
        "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
        "orientation": "auto",
        "textMode": "auto",
        "colorMode": "value",
        "graphMode": "none",
        "justifyMode": "auto",
        "noValue": "no data",
    }
    return p


def row(title, y):
    return {
        "id": nid(),
        "type": "row",
        "title": title,
        "gridPos": {"x": 0, "y": y, "w": 24, "h": 1},
        "collapsed": False,
        "panels": [],
    }


panels = []

# ── Row: Overview ────────────────────────────────────────────────
panels.append(row("Overview", 0))

g = base("gauge", "Health score", 0, 1, 4, 6,
         "0–100, static checks + run history. See docs/honesty.md for what gates the graded score.")
g["targets"] = [target('gha_doctor_health_score_points{repo="$repo"}', instant=True)]
g["fieldConfig"] = field(unit="none", min_=0, max_=100,
                         ths=thresholds(("red", None), ("yellow", 50), ("green", 80)))
g["options"] = {
    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
    "showThresholdLabels": False, "showThresholdMarkers": True,
}
panels.append(g)

panels.append(stat(
    "Findings", 4, 1, 4, 6,
    'gha_doctor_findings{repo="$repo"}',
    unit="none",
    ths=thresholds(("green", None), ("yellow", 1)),
    desc="Current static findings by severity.",
    legend="{{severity}}"))

panels.append(stat(
    "Runs sampled", 8, 1, 4, 3,
    'gha_doctor_runs_sampled{repo="$repo"}', unit="none",
    desc="Workflow runs in the analysis sample."))

panels.append(stat(
    "Sample window", 8, 4, 4, 3,
    'gha_doctor_last_run_timestamp_seconds{repo="$repo"} - gha_doctor_sample_since_timestamp_seconds{repo="$repo"}',
    unit="s",
    desc="Oldest sampled run to report time. Short windows make the per-month framings in the report unreliable; gha-doctor itself refuses to project from <3 days."))

panels.append(stat(
    "Report age", 12, 1, 4, 3,
    'time() - gha_doctor_last_run_timestamp_seconds{repo="$repo"}',
    unit="s",
    ths=thresholds(("green", None), ("yellow", 172800), ("red", 604800)),
    desc="Since the scrape file was generated. If this grows, the scheduled gha-doctor job feeding the textfile collector has stopped."))

panels.append(stat(
    "Runs missing job data", 12, 4, 4, 3,
    'gha_doctor_runs_missing_job_data{repo="$repo"}',
    unit="none",
    ths=thresholds(("green", None), ("orange", 1)),
    desc="When >0, job-derived gauges (queue, cost, waste, flakiness) understate. Usually rate limiting; give the job a token."))

panels.append(stat(
    "Estimated compute value (sample)", 16, 1, 4, 3,
    'gha_doctor_estimated_cost_usd{repo="$repo"}',
    unit="currencyUSD",
    desc="At GitHub's hosted-runner rates; self-hosted jobs excluded. Public repos ride free runners — value, not an invoice."))

panels.append(stat(
    "Wasted share of compute", 16, 4, 4, 3,
    'gha_doctor_wasted_cost_usd{repo="$repo"} / gha_doctor_estimated_cost_usd{repo="$repo"}',
    unit="percentunit",
    ths=thresholds(("green", None), ("yellow", 0.10), ("red", 0.25)),
    desc="Failed runs + retried attempts as a share of the sample's compute."))

panels.append(stat(
    "Flaky jobs", 20, 1, 2, 3,
    'gha_doctor_flaky_jobs{repo="$repo"}',
    unit="none", ths=thresholds(("green", None), ("orange", 1)),
    desc="Jobs that both failed and succeeded on the same commit within the sample. Run gha-doctor --flaky-logs to name the tests."))

panels.append(stat(
    "Zombie crons", 22, 1, 2, 3,
    'gha_doctor_zombie_crons{repo="$repo"}',
    unit="none", ths=thresholds(("green", None), ("red", 1)),
    desc="Scheduled workflows whose newest sampled runs are an unbroken failure streak. Revive or retire."))

panels.append(stat(
    "Self-hosted jobs", 20, 4, 2, 3,
    'gha_doctor_self_hosted_jobs{repo="$repo"}',
    unit="none",
    desc="Sampled jobs on self-hosted runners — excluded from every cost estimate."))

panels.append(stat(
    "Superseded runs completed", 22, 4, 2, 3,
    'gha_doctor_superseded_completed_runs{repo="$repo"}',
    unit="none", ths=thresholds(("green", None), ("yellow", 1)),
    desc="PR runs a newer push made obsolete that ran to completion anyway — what D001's cancel-in-progress prevents."))

# ── Row: Workflows ───────────────────────────────────────────────
panels.append(row("Workflows", 7))

bg = base("bargauge", "Success ratio (decisive runs)", 0, 8, 8, 8,
          "Successful share of runs that reached a verdict. Workflows with no decisive run in the sample are absent, not zero.")
bg["targets"] = [target('gha_doctor_workflow_success_ratio{repo="$repo"}', "{{workflow}}", instant=True)]
bg["fieldConfig"] = field(unit="percentunit", min_=0, max_=1,
                          ths=thresholds(("red", None), ("yellow", 0.8), ("green", 0.95)))
bg["options"] = {
    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
    "orientation": "horizontal", "displayMode": "gradient", "showUnfilled": True,
}
panels.append(bg)

ts = base("timeseries", "Run duration p50 → p95", 8, 8, 8, 8,
          "Per workflow, decisive runs only. Snapshots accumulate into a trend as your scheduled gha-doctor job keeps writing the file.")
ts["targets"] = targets(
    target('gha_doctor_workflow_duration_p50_seconds{repo="$repo"}', "{{workflow}} p50"),
    target('gha_doctor_workflow_duration_p95_seconds{repo="$repo"}', "{{workflow}} p95"),
)
ts["fieldConfig"] = field(unit="s")
ts["options"] = {"legend": {"displayMode": "list", "placement": "bottom"},
                 "tooltip": {"mode": "multi", "sort": "desc"}}
panels.append(ts)

qs = base("timeseries", "Avg queue time per workflow", 16, 8, 4, 8,
          "Mean time a job waited for a runner.")
qs["targets"] = [target('gha_doctor_workflow_avg_queue_seconds{repo="$repo"}', "{{workflow}}")]
qs["fieldConfig"] = field(unit="s")
qs["options"] = {"legend": {"displayMode": "list", "placement": "bottom"},
                 "tooltip": {"mode": "multi", "sort": "desc"}}
panels.append(qs)

cw = base("bargauge", "Compute value per workflow", 20, 8, 4, 8,
          "Sampled runs, hosted-runner rates; self-hosted excluded.")
cw["targets"] = [target('gha_doctor_workflow_estimated_cost_usd{repo="$repo"}', "{{workflow}}", instant=True)]
cw["fieldConfig"] = field(unit="currencyUSD", ths=thresholds(("blue", None)))
cw["options"] = {
    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
    "orientation": "horizontal", "displayMode": "gradient", "showUnfilled": True,
}
panels.append(cw)

# ── Row: Waste ───────────────────────────────────────────────────
panels.append(row("Waste", 16))

wt = base("timeseries", "Where compute goes to die", 0, 17, 12, 8,
          "Billable-weighted seconds across the sample. Failed runs and retries are gha-doctor's waste bucket; the round-up tax is billed-but-unused ceil-to-the-minute time.")
wt["targets"] = targets(
    target('gha_doctor_waste_failed_run_seconds{repo="$repo"}', "failed runs"),
    target('gha_doctor_waste_retry_seconds{repo="$repo"}', "retried attempts"),
    target('gha_doctor_rounding_seconds{repo="$repo"}', "round-up tax"),
    target('gha_doctor_superseded_wasted_seconds{repo="$repo"}', "superseded PR runs"),
)
wt["fieldConfig"] = field(unit="s")
wt["options"] = {"legend": {"displayMode": "list", "placement": "bottom"},
                 "tooltip": {"mode": "multi", "sort": "desc"}}
panels.append(wt)

wc = base("timeseries", "Waste in dollars", 12, 17, 12, 8,
          "Value of the waste and round-up buckets at hosted-runner rates.")
wc["targets"] = targets(
    target('gha_doctor_wasted_cost_usd{repo="$repo"}', "failed + retried"),
    target('gha_doctor_rounding_cost_usd{repo="$repo"}', "round-up tax"),
    target('gha_doctor_superseded_wasted_cost_usd{repo="$repo"}', "superseded PR runs"),
)
wc["fieldConfig"] = field(unit="currencyUSD")
wc["options"] = {"legend": {"displayMode": "list", "placement": "bottom"},
                 "tooltip": {"mode": "multi", "sort": "desc"}}
panels.append(wc)

# ── Row: Cache & artifacts ───────────────────────────────────────
panels.append(row("Cache & artifacts", 25))

cg = base("gauge", "Cache vs 10 GB soft limit", 0, 26, 5, 6,
          "Above 1.0 GitHub evicts by LRU — hit rates degrade. gha-doctor's report names the biggest offenders.")
cg["targets"] = [target('gha_doctor_cache_limit_ratio{repo="$repo"}', instant=True)]
cg["fieldConfig"] = field(unit="percentunit", min_=0, max_=1.2,
                          ths=thresholds(("green", None), ("yellow", 0.8), ("red", 1.0)))
cg["options"] = {
    "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
    "showThresholdLabels": False, "showThresholdMarkers": True,
}
panels.append(cg)

panels.append(stat(
    "Cache size", 5, 26, 4, 3,
    'gha_doctor_cache_size_bytes{repo="$repo"}', unit="bytes",
    desc="Total Actions cache storage currently held."))
panels.append(stat(
    "Cache entries", 5, 29, 4, 3,
    'gha_doctor_cache_entries{repo="$repo"}', unit="none"))
panels.append(stat(
    "Stale cache (7d+ untouched)", 9, 26, 4, 3,
    'gha_doctor_cache_stale_bytes{repo="$repo"}', unit="bytes",
    ths=thresholds(("green", None), ("yellow", 1073741824)),
    desc="Examined entries; see the report for sampling notes."))
panels.append(stat(
    "PR-ref cache (unreachable)", 9, 29, 4, 3,
    'gha_doctor_cache_pr_ref_bytes{repo="$repo"}', unit="bytes",
    ths=thresholds(("green", None), ("yellow", 1073741824)),
    desc="Held by refs/pull/* keys — unreachable from other branches."))
panels.append(stat(
    "Artifact entries", 13, 26, 4, 3,
    'gha_doctor_artifact_entries{repo="$repo"}', unit="none",
    desc="Including expired artifacts still indexed."))

info = base("text", "About this dashboard", 13, 29, 11, 3)
info["options"] = {"mode": "markdown", "content": (
    "Fed by [gha-doctor](https://github.com/linnea-bakshi/gha-doctor) `--prom` "
    "via node_exporter's textfile collector or a Pushgateway. "
    "**An absent series is not a zero** — unmeasured aggregates are simply missing, so "
    "\u201cNo data\u201d here means *not measured*, not *fine* "
    "([honesty policy](https://linnea-bakshi.github.io/gha-doctor/honesty))."
)}
panels.append(info)

dash = {
    "uid": "gha-doctor",
    "title": "gha-doctor — GitHub Actions CI health",
    "description": "CI health, waste and cache telemetry from gha-doctor --prom. https://github.com/linnea-bakshi/gha-doctor",
    "tags": ["gha-doctor", "github-actions", "ci"],
    "timezone": "browser",
    "editable": True,
    "graphTooltip": 1,
    "time": {"from": "now-24h", "to": "now"},
    "refresh": "",
    "schemaVersion": 39,
    "templating": {"list": [
        {
            "name": "datasource",
            "label": "Data source",
            "type": "datasource",
            "query": "prometheus",
            "current": {},
            "hide": 0,
        },
        {
            "name": "repo",
            "label": "Repository",
            "type": "query",
            "datasource": DS,
            "query": "label_values(gha_doctor_info, repo)",
            "refresh": 2,
            "sort": 1,
            "current": {},
            "hide": 0,
        },
    ]},
    "annotations": {"list": []},
    "links": [
        {"title": "gha-doctor docs", "type": "link",
         "url": "https://linnea-bakshi.github.io/gha-doctor/", "targetBlank": True},
    ],
    "panels": panels,
}

out = os.path.join(os.path.dirname(__file__), "..", "docs", "grafana",
                   "gha-doctor-dashboard.json")
with open(out, "w") as f:
    json.dump(dash, f, indent=2)
    f.write("\n")
print("panels:", len(panels), "bytes:", len(json.dumps(dash)))
