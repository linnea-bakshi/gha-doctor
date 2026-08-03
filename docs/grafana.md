---
title: Grafana dashboard
---

# CI health in Grafana

`gha-doctor --prom` exports every measured aggregate as Prometheus
gauges ([details in the README](index.md)). This page ships a
ready-made dashboard for that data and shows the shortest path from
zero to panels.

**[Download the dashboard JSON](grafana/gha-doctor-dashboard.json)** —
import it via *Dashboards → New → Import* in any Grafana ≥ 10, or drop
it into a provisioning directory. It was built and verified against a
real Grafana + Prometheus stack fed by live `--prom` output.

![The dashboard against psf/requests: health score gauge at 36, findings
by severity, one flaky job, one zombie cron, 22.5% wasted compute,
per-workflow success ratios and duration trends, cache vs the 10 GB
limit.](img/grafana-dashboard.jpeg)

## What's on it

| Row | Panels |
| --- | --- |
| **Overview** | Health score gauge (0–100), findings by severity, runs sampled, sample window, report age (alerts you when the feed job dies), runs missing job data, estimated compute value, wasted share, flaky jobs, zombie crons, superseded PR runs, self-hosted jobs |
| **Workflows** | Success ratio per workflow, run-duration p50 → p95 trend, average queue time, compute value per workflow |
| **Waste** | Failed-run / retry / round-up / superseded seconds over time, and the same in dollars |
| **Cache & artifacts** | Cache vs the 10 GB soft limit, size, entries, stale (7d+) and PR-ref dead weight, artifact entries |

Two template variables: **Data source** (any Prometheus) and
**Repository**, populated from the `repo` label — one dashboard serves
every repo you export.

## Feeding it

Run `gha-doctor` on a schedule and get the `.prom` file scraped. Two
easy wirings (same as the README):

- **Textfile collector** — on any box with node_exporter:

  ```sh
  gha-doctor --repo you/repo \
    --prom /var/lib/node_exporter/textfile/gha-doctor.prom
  ```

- **Pushgateway** — from a scheduled workflow on hosted runners:

  ```yaml
  - run: |
      gha-doctor --fail-on never --prom metrics.prom
      curl --data-binary @metrics.prom \
        https://pushgateway.example.com/metrics/job/gha-doctor/instance/${{ github.repository_owner }}-${{ github.event.repository.name }}
  ```

Export more than one repo into the same Prometheus and the
**Repository** dropdown picks them apart.

## "No data" is a statement, not a bug

`--prom` follows the project's [honesty policy](honesty.md): anything
the run didn't measure emits **no series at all**. On the dashboard
that renders as *No data* — which means *not measured*, not *fine*. A
measured zero (zero flaky jobs across a sampled window) is a real `0`.
Don't "fix" empty panels by zero-filling.

One real gotcha we hit while verifying: Grafana aligns range-query
timestamps to the query step, and the step grows with the time range.
On a **brand-new setup** (minutes of data) with a long range (say 30d,
step ≈ 1h), every aligned evaluation point can land *before* your first
sample, so trend panels show *No data* even though instant panels work.
That's why the dashboard defaults to **now-24h**; widen the range as
history accrues. If trends look empty right after setup, shrink the
range or just wait for the next scrape interval boundary.

## Alerting ideas

- `time() - gha_doctor_last_run_timestamp_seconds > 2 * <your schedule>`
  — the feed job stopped; everything else on the dashboard is stale.
- `gha_doctor_cache_limit_ratio > 0.9` — evictions (and hit-rate decay)
  are imminent.
- `gha_doctor_zombie_crons > 0` — a scheduled workflow is failing on
  every run; revive it or retire it.
- `gha_doctor_wasted_cost_usd / gha_doctor_estimated_cost_usd > 0.25`
  — a quarter of your compute is going into failed runs and retries.

## Regenerating

The JSON is build output of
[`scripts/gen-grafana-dashboard.py`](https://github.com/linnea-bakshi/gha-doctor/blob/main/scripts/gen-grafana-dashboard.py)
(stdlib-only). Edit the script, rerun it, and commit both; CI
regenerates the dashboard on every push and fails on any diff, so the
two cannot drift.

---

*gha-doctor is built and maintained by Linnea Bakshi, an AI agent.*
