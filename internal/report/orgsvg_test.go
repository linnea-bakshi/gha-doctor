package report

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/linnea-bakshi/gha-doctor/internal/api"
)

func testNow() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }

func orgFixture(nRepos int) *api.OrgAnalysis {
	oa := &api.OrgAnalysis{
		Org:           "acme",
		ReposListed:   nRepos + 3,
		ReposScanned:  nRepos,
		TotalEst30d:   1234,
		TotalFailRate: 0.17,
	}
	for i := 0; i < nRepos; i++ {
		oa.Repos = append(oa.Repos, api.OrgRepoStats{
			Repo:          fmt.Sprintf("repo-%02d", i),
			RunsSampled:   100 - i,
			FailRate:      float64(i) * 0.05,
			P50Minutes:    3.5,
			Est30dMinutes: float64(1000 - i*10),
			LastRun:       testNow().Add(-time.Duration(i+1) * time.Hour),
		})
	}
	return oa
}

func TestOrgSVGWellFormedAndPinned(t *testing.T) {
	var buf bytes.Buffer
	if err := OrgSVG(&buf, orgFixture(3), testNow()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if err := xml.Unmarshal(buf.Bytes(), new(struct{})); err != nil {
		t.Fatalf("not well-formed XML: %v\n%s", err, out)
	}
	for _, want := range []string{
		"CI checkup: acme",
		"3 of 6 repos scanned",
		"repo-00", "repo-02",
		"1h ago", "3h ago",
		"total ~1234 run min/30d",
		"run-weighted fail 17%",
		"gha-doctor",
		"textLength=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in card:\n%s", want, out)
		}
	}
	// fail-rate coloring: repo-00 (0%) green, repo-02 (10%) yellow
	if !strings.Contains(out, "#3fb950") || !strings.Contains(out, "#d29922") {
		t.Errorf("expected green and yellow fail cells:\n%s", out)
	}
}

func TestOrgSVGCapsRowsAndAggregatesTail(t *testing.T) {
	oa := orgFixture(20)
	var buf bytes.Buffer
	if err := OrgSVG(&buf, oa, testNow()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "repo-11") {
		t.Error("12th row (repo-11) should be shown")
	}
	if strings.Contains(out, "repo-12") {
		t.Error("13th row should be aggregated, not shown")
	}
	if !strings.Contains(out, "and 8 more active repos") {
		t.Errorf("missing tail aggregation line:\n%s", out)
	}
	// tail minutes: repos 12..19 → 1000-120 … 1000-190
	want := 0.0
	for i := 12; i < 20; i++ {
		want += float64(1000 - i*10)
	}
	if !strings.Contains(out, fmt.Sprintf(">%.0f<", want)) {
		t.Errorf("missing aggregated tail minutes %.0f:\n%s", want, out)
	}
	if len(oa.Repos) != 20 {
		t.Error("OrgSVG must not mutate the analysis")
	}
}

func TestOrgSVGEmpty(t *testing.T) {
	oa := &api.OrgAnalysis{Org: "ghost", ReposListed: 2, ReposScanned: 2}
	var buf bytes.Buffer
	if err := OrgSVG(&buf, oa, testNow()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if err := xml.Unmarshal(buf.Bytes(), new(struct{})); err != nil {
		t.Fatalf("not well-formed XML: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no completed workflow runs") || !strings.Contains(out, "no run data") {
		t.Errorf("empty-state text missing:\n%s", out)
	}
}

func TestOrgSVGEscapesRepoNames(t *testing.T) {
	oa := orgFixture(1)
	oa.Repos[0].Repo = `we<i>rd&"name`
	var buf bytes.Buffer
	if err := OrgSVG(&buf, oa, testNow()); err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(buf.Bytes(), new(struct{})); err != nil {
		t.Fatalf("escaping failed, XML broken: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), `we<i>rd`) {
		t.Error("raw < leaked into SVG")
	}
}
