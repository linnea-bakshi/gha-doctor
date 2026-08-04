package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

func sampleOrg() *api.OrgAnalysis {
	return &api.OrgAnalysis{
		Org: "acme", ReposListed: 5, ReposScanned: 3,
		SkippedForks: 1, SkippedArch: 1, QuietRepos: 1,
		Repos: []api.OrgRepoStats{
			{Repo: "api", RunsSampled: 50, FailRate: 0.42, P50Minutes: 6.5, P95Minutes: 14.2,
				Est30dMinutes: 900, Extrapolated: true, LastRun: time.Now().Add(-3 * time.Hour)},
			{Repo: "web", RunsSampled: 20, FailRate: 0.05, P50Minutes: 3.1, P95Minutes: 8.0,
				Est30dMinutes: 120, LastRun: time.Now().Add(-72 * time.Hour)},
		},
		TotalEst30d: 1020, TotalFailRate: 0.31,
		ZombieCrons: []api.OrgZombieCron{
			{Repo: "api", Workflow: "Lock inactive issues", URL: "https://example.test/zrun",
				Fails: 70, StreakOpen: true, SpanDays: 25.3,
				LastFailedAt: time.Date(2026, 8, 4, 0, 22, 0, 0, time.UTC)},
		},
		ZombieCronsMore: 1,
	}
}

func TestOrgTerminal(t *testing.T) {
	var buf bytes.Buffer
	Org(&buf, Style{Plain: true}, sampleOrg())
	out := buf.String()
	for _, want := range []string{
		"Org checkup: acme", "3 of 5 repos", "1 forks", "1 archived",
		"api", "42%", "900*", "web", "~1020", "extrapolated",
		"no completed runs", "wall-clock minutes ≠ billable",
		"Failing scheduled workflows",
		"api: Lock inactive issues — ≥ 70 consecutive scheduled failures over 25 days",
		"last failed 2026-08-04 — https://example.test/zrun",
		"…and 1 more (full list in --json)",
		"these streaks inflate the fail column above",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q\n%s", want, out)
		}
	}
}

func TestOrgTerminalEmpty(t *testing.T) {
	var buf bytes.Buffer
	Org(&buf, Style{Plain: true}, &api.OrgAnalysis{Org: "ghost", ReposListed: 2, ReposScanned: 2, QuietRepos: 2})
	if out := buf.String(); !strings.Contains(out, "no completed workflow runs") {
		t.Errorf("empty org output missing placeholder:\n%s", out)
	}
}

func TestOrgJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := OrgJSON(&buf, sampleOrg()); err != nil {
		t.Fatal(err)
	}
	var back api.OrgAnalysis
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Org != "acme" || len(back.Repos) != 2 || !back.Repos[0].Extrapolated {
		t.Errorf("round trip mismatch: %+v", back)
	}
	if len(back.ZombieCrons) != 1 || back.ZombieCrons[0].Fails != 70 ||
		!back.ZombieCrons[0].StreakOpen || back.ZombieCronsMore != 1 {
		t.Errorf("zombie crons lost in round trip: %+v", back.ZombieCrons)
	}
}

func TestOrgMarkdown(t *testing.T) {
	var buf bytes.Buffer
	OrgMarkdown(&buf, sampleOrg())
	out := buf.String()
	for _, want := range []string{"## Org checkup: acme", "| api | 50 | 42% |", "900\\*", "~1020",
		"**Failing scheduled workflows**",
		"- api: [Lock inactive issues](https://example.test/zrun) — ≥ 70 consecutive scheduled failures over 25 days, last 2026-08-04",
		"- …and 1 more (full list in `--json`)",
		"_These streaks inflate the fail column above.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n%s", want, out)
		}
	}
}
