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

// FindUpdateConfig locates dependabot/renovate config in a remote repo via
// the contents API: one listing of the repo root, one of .github/, plus one
// file fetch if a dependabot config exists (2-3 requests). A missing
// dependabot config returns nil; a missing renovate config returns "".
func (c *Client) FindUpdateConfig(owner, repo string) (dependabot *WorkflowFile, renovatePath string, err error) {
	var root []contentsEntry
	if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/", owner, repo), nil, &root); err != nil {
		return nil, "", err
	}
	hasGithubDir := false
	for _, e := range root {
		if e.Type == "file" && renovateRootNames[e.Name] {
			return nil, e.Path, nil
		}
		if e.Type == "dir" && e.Name == ".github" {
			hasGithubDir = true
		}
	}
	if !hasGithubDir {
		return nil, "", nil
	}
	var gh []contentsEntry
	if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/.github", owner, repo), nil, &gh); err != nil {
		if _, ok := err.(*NotFoundError); ok {
			return nil, "", nil
		}
		return nil, "", err
	}
	dependabotFile := ""
	for _, e := range gh {
		if e.Type != "file" {
			continue
		}
		if renovateGithubNames[e.Name] {
			return nil, e.Path, nil
		}
		if e.Name == "dependabot.yml" || e.Name == "dependabot.yaml" {
			dependabotFile = e.Path
		}
	}
	if dependabotFile == "" {
		return nil, "", nil
	}
	var f contentsEntry
	if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, dependabotFile), nil, &f); err != nil {
		return nil, "", fmt.Errorf("fetching %s: %w", dependabotFile, err)
	}
	if f.Encoding != "base64" {
		return nil, "", nil // pathologically large config; skip the check
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return nil, "", fmt.Errorf("decoding %s: %w", dependabotFile, err)
	}
	return &WorkflowFile{Path: dependabotFile, Data: data}, "", nil
}
