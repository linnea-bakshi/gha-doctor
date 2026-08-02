#!/usr/bin/env bash
# Collector for the runtime waste study: runs the full gha-doctor history
# analysis (--runs 100 --flaky-logs 4) against the top-N most-starred GitHub
# repos and caches per-repo JSON for scripts/waste-study.sh to aggregate.
#
# Resumable: re-running skips repos already cached. Paced against the
# authenticated core rate limit (needs `gh auth` or GITHUB_TOKEN).
#
# Usage:
#   scripts/waste-study-collect.sh            # top 250, cache /tmp/waste-cache
#   N=50 CACHE=/tmp/w scripts/waste-study-collect.sh
set -u
N="${N:-250}"
CACHE="${CACHE:-/tmp/waste-cache}"
RUNS="${RUNS:-100}"
FLAKYLOGS="${FLAKYLOGS:-4}"
MIN_REMAINING="${MIN_REMAINING:-600}"
mkdir -p "$CACHE"

# ---- 1. Top-N most-starred repos (search API, 100/page) -------------------
repos_file="$CACHE/repos.txt"
if [ ! -s "$repos_file" ]; then
  pages=$(( (N + 99) / 100 ))
  for p in $(seq 1 "$pages"); do
    gh api "search/repositories?q=stars:%3E10000&sort=stars&order=desc&per_page=100&page=$p" \
      --jq '.items[].full_name'
    sleep 2 # search API secondary limit
  done | head -n "$N" >"$repos_file"
fi
total=$(wc -l <"$repos_file")
echo "waste sweep: $total repos (cache: $CACHE, runs=$RUNS)" >&2

# NB: the /rate_limit endpoint can serve stale numbers (observed: it said
# 4,807 core remaining while live request headers said 0). Trust only the
# X-RateLimit-Remaining header of a real request (costs 1 core call).
wait_for_budget() {
  tok="${GITHUB_TOKEN:-$(gh auth token 2>/dev/null)}"
  while :; do
    rem=$(curl -s -o /dev/null -D - -H "Authorization: Bearer $tok" \
      https://api.github.com/user 2>/dev/null \
      | tr -d '\r' | awk -F': ' 'tolower($1)=="x-ratelimit-remaining"{print $2}')
    [ "${rem:-0}" -ge "$MIN_REMAINING" ] 2>/dev/null && return
    echo "  core remaining=${rem:-?} < $MIN_REMAINING; sleeping 300s…" >&2
    sleep 300
  done
}

# ---- 2. Analyze each repo's run history (no clone) ------------------------
i=0
while read -r r; do
  i=$((i+1))
  f="$CACHE/$(echo "$r" | tr '/' '=').json"
  [ -s "$f" ] && continue
  err="$CACHE/$(echo "$r" | tr '/' '=').err"
  [ -s "$err" ] && continue   # previously errored (e.g. no runs); keep record
  wait_for_budget
  echo "[$i/$total] $r" >&2
  gha-doctor --repo "$r" --no-config --runs "$RUNS" --flaky-logs "$FLAKYLOGS" --json >"$f" 2>"$err"
  rc=$?
  case $rc in
    0|2) rm -f "$err" ;;
    *) rm -f "$f"; echo "  rc=$rc: $(tail -1 "$err" 2>/dev/null | head -c 160)" >&2 ;;
  esac
done <"$repos_file"
echo "done: $(ls "$CACHE" | grep -c '\.json$') cached, $(ls "$CACHE" | grep -c '\.err$') errored" >&2
