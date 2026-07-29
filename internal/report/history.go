package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// ScoreEntry is one recorded health-score measurement. Entries are stored
// as JSON Lines so the history file appends cleanly and git diffs show
// exactly one new line per run.
type ScoreEntry struct {
	Time       time.Time          `json:"ts"`
	Repo       string             `json:"repo,omitempty"`
	Points     int                `json:"points"`
	Grade      string             `json:"grade"`
	Basis      string             `json:"basis"`
	Components map[string]float64 `json:"components,omitempty"` // name → points deducted
}

// ScoreDelta describes how the score moved since the previous recorded
// entry for the same repo.
type ScoreDelta struct {
	Prev         ScoreEntry        `json:"prev"`
	Change       int               `json:"change"` // current − previous points
	BasisChanged bool              `json:"basis_changed,omitempty"`
	Improved     []ComponentChange `json:"improved,omitempty"`
	Regressed    []ComponentChange `json:"regressed,omitempty"`
}

// ComponentChange is one component whose deduction moved between runs.
type ComponentChange struct {
	Name string  `json:"name"`
	From float64 `json:"from"` // points deducted before
	To   float64 `json:"to"`   // points deducted now
}

// EntryFor converts a computed Score into a history entry.
func EntryFor(sc Score, repo string, now time.Time) ScoreEntry {
	e := ScoreEntry{
		Time:   now.UTC().Truncate(time.Second),
		Repo:   repo,
		Points: sc.Points,
		Grade:  sc.Grade,
		Basis:  sc.Basis,
	}
	if len(sc.Components) > 0 {
		e.Components = make(map[string]float64, len(sc.Components))
		for _, c := range sc.Components {
			e.Components[c.Name] = c.Deducted
		}
	}
	return e
}

// LoadHistory reads a JSONL score-history file. Unparseable lines are
// skipped (counted in the second return) rather than failing the run: a
// corrupt line in a committed history file should not break CI.
func LoadHistory(path string) ([]ScoreEntry, int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()
	var entries []ScoreEntry
	bad := 0
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		var e ScoreEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Time.IsZero() {
			bad++
			continue
		}
		entries = append(entries, e)
	}
	return entries, bad, scan.Err()
}

// AppendHistory appends one entry to the JSONL history file, creating it
// if needed.
func AppendHistory(path string, e ScoreEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// LatestFor returns the most recent entry for the given repo. Entries
// recorded without a repo (older files, or unknown remotes) match any
// repo, so a history file that predates repo tagging still compares.
func LatestFor(entries []ScoreEntry, repo string) (ScoreEntry, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Repo == "" || repo == "" || strings.EqualFold(entries[i].Repo, repo) {
			return entries[i], true
		}
	}
	return ScoreEntry{}, false
}

// DeltaFrom compares the current score with a previous entry.
func DeltaFrom(prev ScoreEntry, cur Score) *ScoreDelta {
	d := &ScoreDelta{
		Prev:         prev,
		Change:       cur.Points - prev.Points,
		BasisChanged: prev.Basis != cur.Basis,
	}
	// Component movers: only components present in both runs are
	// comparable; appearing/disappearing components usually mean the
	// basis changed (e.g. --lint-only vs full run), which is flagged
	// separately rather than reported as a regression.
	if len(prev.Components) > 0 {
		for _, c := range cur.Components {
			from, ok := prev.Components[c.Name]
			if !ok {
				continue
			}
			diff := c.Deducted - from
			if math.Abs(diff) < 0.5 {
				continue
			}
			ch := ComponentChange{Name: c.Name, From: from, To: c.Deducted}
			if diff < 0 {
				d.Improved = append(d.Improved, ch)
			} else {
				d.Regressed = append(d.Regressed, ch)
			}
		}
	}
	return d
}

// deltaLine renders a one-line summary of the change, e.g.
// "+7 since 2026-07-22 (B 84 → A 91)".
func deltaLine(d *ScoreDelta, cur Score) string {
	when := d.Prev.Time.Format("2006-01-02")
	switch {
	case d.Change == 0:
		return fmt.Sprintf("unchanged since %s (%s %d)", when, d.Prev.Grade, d.Prev.Points)
	default:
		return fmt.Sprintf("%+d since %s (%s %d → %s %d)",
			d.Change, when, d.Prev.Grade, d.Prev.Points, cur.Grade, cur.Points)
	}
}

func changeList(chs []ComponentChange) string {
	parts := make([]string, len(chs))
	for i, c := range chs {
		parts[i] = fmt.Sprintf("%s (−%s → −%s)", c.Name, trimZero(c.From), trimZero(c.To))
	}
	return strings.Join(parts, ", ")
}
