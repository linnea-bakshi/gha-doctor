package api

import (
	"fmt"
	"sort"
	"strings"
)

// MatrixStats summarizes matrix-job shard balance across the sampled runs.
// A matrix group is every job sharing a base name plus a "(...)" matrix
// suffix within one workflow — e.g. `test (ubuntu-latest, 3.12)`.
//
// Imbalance is a pure wall-clock problem: the group finishes when its
// slowest shard does, so billable minutes are unchanged but PR feedback
// waits on the straggler. The "ideal" is the per-run mean shard duration —
// the lower bound a perfectly even split of the same work would reach.
type MatrixStats struct {
	// GroupsMeasured counts matrix groups that passed the measurement
	// gate (enough clean runs to take a median), imbalanced or not.
	GroupsMeasured int `json:"groups_measured"`
	// Imbalanced lists groups whose median wall/ideal ratio and saving
	// clear the reporting thresholds, worst first.
	Imbalanced []MatrixGroup `json:"imbalanced,omitempty"`
}

// MatrixGroup is one matrix job family with imbalance figures.
// All medians are taken across measured runs (runs where every non-skipped
// shard of the group succeeded — failed shards stop early and would fake
// imbalance).
type MatrixGroup struct {
	Workflow     string  `json:"workflow"`
	Job          string  `json:"job"`    // base job name without the matrix suffix
	Shards       int     `json:"shards"` // modal shard count per run
	RunsMeasured int     `json:"runs_measured"`
	P50WallMin   float64 `json:"p50_wall_minutes"`   // median per-run slowest shard
	P50IdealMin  float64 `json:"p50_ideal_minutes"`  // median per-run mean shard (even-split lower bound)
	P50SavingMin float64 `json:"p50_saving_minutes"` // median per-run (wall − ideal)
	Ratio        float64 `json:"imbalance_ratio"`    // P50Wall / P50Ideal
	SlowestShard string  `json:"slowest_shard"`      // matrix suffix of the slowest shard, e.g. "(windows-latest, 3.12)"
	SlowestP50   float64 `json:"slowest_shard_p50_minutes"`
	FastestShard string  `json:"fastest_shard"`
	FastestP50   float64 `json:"fastest_shard_p50_minutes"`
}

// Measurement/reporting gates. A group needs matrixMinRuns clean runs and
// matrixMinShards shards before a median means anything (2 shards of
// different platforms are expected to differ — that's a matrix, not an
// imbalance); it is only reported when the straggler costs at least
// matrixMinRatio× the even-split time AND matrixMinSavingMin real minutes
// per run (a 2× ratio on a 20-second job is noise).
const (
	matrixMinRuns      = 5
	matrixMinShards    = 3
	matrixMinRatio     = 1.5
	matrixMinSavingMin = 1.0
	matrixMaxGroups    = 5
)

// matrixSuffix returns the "(...)" matrix part of a job name, or "".
func matrixSuffix(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[i+1:]
	}
	return ""
}

// computeMatrixBalance measures shard balance for every matrix job group.
func (a *Analysis) computeMatrixBalance(runs []Run, jobsByRun map[int64][]Job) {
	type groupKey struct{ workflow, job string }
	type groupAcc struct {
		walls, ideals, savings []float64
		shardCounts            []int
		byShard                map[string][]float64 // suffix -> durations
	}
	groups := map[groupKey]*groupAcc{}

	for _, r := range runs {
		jobs := jobsByRun[r.ID]
		// Only the latest attempt: earlier attempts overlap in time and
		// double-count shards.
		maxAttempt := 0
		for _, j := range jobs {
			if j.RunAttempt > maxAttempt {
				maxAttempt = j.RunAttempt
			}
		}
		// Bucket this run's matrix jobs by base name.
		type runGroup struct {
			durs     []float64
			suffixes []string
			clean    bool // every non-skipped shard succeeded
		}
		byBase := map[string]*runGroup{}
		for _, j := range jobs {
			if j.RunAttempt != maxAttempt {
				continue
			}
			suffix := matrixSuffix(j.Name)
			if suffix == "" {
				continue // not a matrix job
			}
			if j.Conclusion == "skipped" {
				continue // conditional/excluded shard; doesn't gate the run
			}
			base := baseJobName(j.Name)
			g := byBase[base]
			if g == nil {
				g = &runGroup{clean: true}
				byBase[base] = g
			}
			if j.Conclusion != "success" || j.CompletedAt.IsZero() || !j.CompletedAt.After(j.StartedAt) {
				g.clean = false
				continue
			}
			g.durs = append(g.durs, j.CompletedAt.Sub(j.StartedAt).Minutes())
			g.suffixes = append(g.suffixes, suffix)
		}
		for base, g := range byBase {
			if !g.clean || len(g.durs) < 2 {
				continue
			}
			key := groupKey{r.Name, base}
			acc := groups[key]
			if acc == nil {
				acc = &groupAcc{byShard: map[string][]float64{}}
				groups[key] = acc
			}
			wall, sum := 0.0, 0.0
			for _, d := range g.durs {
				sum += d
				if d > wall {
					wall = d
				}
			}
			ideal := sum / float64(len(g.durs))
			acc.walls = append(acc.walls, wall)
			acc.ideals = append(acc.ideals, ideal)
			acc.savings = append(acc.savings, wall-ideal)
			acc.shardCounts = append(acc.shardCounts, len(g.durs))
			for i, d := range g.durs {
				acc.byShard[g.suffixes[i]] = append(acc.byShard[g.suffixes[i]], d)
			}
		}
	}

	ms := MatrixStats{}
	for key, acc := range groups {
		if len(acc.walls) < matrixMinRuns || modalInt(acc.shardCounts) < matrixMinShards {
			continue
		}
		ms.GroupsMeasured++
		sort.Float64s(acc.walls)
		sort.Float64s(acc.ideals)
		sort.Float64s(acc.savings)
		wall := percentile(acc.walls, 0.50)
		ideal := percentile(acc.ideals, 0.50)
		saving := percentile(acc.savings, 0.50)
		if ideal <= 0 {
			continue
		}
		ratio := wall / ideal
		if ratio < matrixMinRatio || saving < matrixMinSavingMin {
			continue
		}
		slowName, fastName := "", ""
		slowP50, fastP50 := 0.0, 0.0
		for suffix, durs := range acc.byShard {
			sort.Float64s(durs)
			p50 := percentile(durs, 0.50)
			if slowName == "" || p50 > slowP50 {
				slowName, slowP50 = suffix, p50
			}
			if fastName == "" || p50 < fastP50 {
				fastName, fastP50 = suffix, p50
			}
		}
		ms.Imbalanced = append(ms.Imbalanced, MatrixGroup{
			Workflow:     key.workflow,
			Job:          key.job,
			Shards:       modalInt(acc.shardCounts),
			RunsMeasured: len(acc.walls),
			P50WallMin:   wall,
			P50IdealMin:  ideal,
			P50SavingMin: saving,
			Ratio:        ratio,
			SlowestShard: slowName,
			SlowestP50:   slowP50,
			FastestShard: fastName,
			FastestP50:   fastP50,
		})
	}
	sort.Slice(ms.Imbalanced, func(i, j int) bool {
		return ms.Imbalanced[i].P50SavingMin > ms.Imbalanced[j].P50SavingMin
	})
	if len(ms.Imbalanced) > matrixMaxGroups {
		ms.Imbalanced = ms.Imbalanced[:matrixMaxGroups]
	}
	if ms.GroupsMeasured > 0 {
		a.Matrix = &ms
	}
}

// modalInt returns the most common value (ties: the larger value, so a
// growing matrix isn't undercounted by its history).
func modalInt(xs []int) int {
	counts := map[int]int{}
	for _, x := range xs {
		counts[x]++
	}
	best, bestN := 0, 0
	for v, n := range counts {
		if n > bestN || (n == bestN && v > best) {
			best, bestN = v, n
		}
	}
	return best
}

// MatrixRatioStr renders the imbalance ratio for display ("3.4×").
func (g MatrixGroup) MatrixRatioStr() string {
	return fmt.Sprintf("%.1f×", g.Ratio)
}
