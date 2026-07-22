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
}

func TestOrgMarkdown(t *testing.T) {
	var buf bytes.Buffer
	OrgMarkdown(&buf, sampleOrg())
	out := buf.String()
	for _, want := range []string{"## Org checkup: acme", "| api | 50 | 42% |", "900\\*", "~1020"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n%s", want, out)
		}
	}
}
