package api

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// renovateRootNames / renovateGithubNames mirror the config locations
// renovate looks for in a GitHub repo. Presence of any counts as "actions
// updates are automated" (renovate's github-actions manager is on by
// default), so D017 stays quiet.
var renovateRootNames = map[string]bool{
	"renovate.json":     true,
	"renovate.json5":    true,
	".renovaterc":       true,
	".renovaterc.json":  true,
	".renovaterc.json5": true,
}

var renovateGithubNames = map[string]bool{
	"renovate.json":  true,
	"renovate.json5": true,
}

// RepoMeta is everything the doctor learns about a remote repo from the
// contents API outside .github/workflows: the root file listing (package-
// manager lockfile detection), the repo's own .gha-doctor.yml if any, and
// its dependency-update automation (dependabot/renovate) for D017. One
// root listing, at most one .github/ listing, plus one fetch per config
// file found — shared by every caller so the requests happen once.
type RepoMeta struct {
	RootFiles    []string      // names of the files at the repo root
	DoctorConfig *WorkflowFile // .gha-doctor.yml wherever found, nil if none
	Dependabot   *WorkflowFile // .github/dependabot.yml, nil if none needed
	RenovatePath string        // path of a renovate config, "" if none
}

// doctorConfigPath picks the repo-config path from the two listings,
// mirroring the precedence in config.Paths (root before .github/, .yml
// before .yaml). Kept here as plain string checks so the api package does
// not depend on the config package.
func doctorConfigPath(rootHas map[string]bool, githubNames []string) string {
	for _, p := range []string{".gha-doctor.yml", ".gha-doctor.yaml"} {
		if rootHas[p] {
			return p
		}
	}
	gh := map[string]bool{}
	for _, n := range githubNames {
		gh[n] = true
	}
	for _, n := range []string{"gha-doctor.yml", "gha-doctor.yaml"} {
		if gh[n] {
			return ".github/" + n
		}
	}
	return ""
}

// FindRepoMeta gathers RepoMeta in 2-4 requests. The .github/ listing is
// skipped when the root already answered every question. Errors from the
// listings propagate so callers can distinguish "checked, absent" from
// "could not check" (D017 must never claim absence without evidence).
func (c *Client) FindRepoMeta(owner, repo string) (*RepoMeta, error) {
	m := &RepoMeta{}
	var root []contentsEntry
	if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/", owner, repo), nil, &root); err != nil {
		return nil, err
	}
	rootHas := map[string]bool{}
	hasGithubDir := false
	for _, e := range root {
		if e.Type == "file" {
			m.RootFiles = append(m.RootFiles, e.Name)
			rootHas[e.Name] = true
			if m.RenovatePath == "" && renovateRootNames[e.Name] {
				m.RenovatePath = e.Path
			}
		}
		if e.Type == "dir" && e.Name == ".github" {
			hasGithubDir = true
		}
	}

	var githubNames []string
	dependabotFile := ""
	rootHasDoctor := rootHas[".gha-doctor.yml"] || rootHas[".gha-doctor.yaml"]
	if hasGithubDir && (m.RenovatePath == "" || !rootHasDoctor) {
		var gh []contentsEntry
		if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/.github", owner, repo), nil, &gh); err != nil {
			if _, ok := err.(*NotFoundError); !ok {
				return nil, err
			}
		} else {
			for _, e := range gh {
				if e.Type != "file" {
					continue
				}
				githubNames = append(githubNames, e.Name)
				if m.RenovatePath == "" && renovateGithubNames[e.Name] {
					m.RenovatePath = e.Path
				}
				if e.Name == "dependabot.yml" || e.Name == "dependabot.yaml" {
					dependabotFile = e.Path
				}
			}
		}
	}

	if p := doctorConfigPath(rootHas, githubNames); p != "" {
		f, err := c.fetchSmallFile(owner, repo, p)
		if err != nil {
			return nil, err
		}
		m.DoctorConfig = f
	}
	if m.RenovatePath == "" && dependabotFile != "" {
		f, err := c.fetchSmallFile(owner, repo, dependabotFile)
		if err != nil {
			return nil, err
		}
		m.Dependabot = f
	}
	return m, nil
}

// fetchSmallFile fetches one file via the contents API. Files too large for
// inline base64 (~1 MB+) return nil: a config that big is pathological, and
// skipping beats failing the whole scan.
func (c *Client) fetchSmallFile(owner, repo, path string) (*WorkflowFile, error) {
	var f contentsEntry
	if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path), nil, &f); err != nil {
		return nil, fmt.Errorf("fetching %s: %w", path, err)
	}
	if f.Encoding != "base64" {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	return &WorkflowFile{Path: path, Data: data}, nil
}

// FindUpdateConfig locates dependabot/renovate config in a remote repo.
// Thin wrapper over FindRepoMeta for callers that only need D017 inputs.
func (c *Client) FindUpdateConfig(owner, repo string) (dependabot *WorkflowFile, renovatePath string, err error) {
	m, err := c.FindRepoMeta(owner, repo)
	if err != nil {
		return nil, "", err
	}
	return m.Dependabot, m.RenovatePath, nil
}
