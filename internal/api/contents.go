package api

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// WorkflowFile is a workflow definition fetched from a remote repository.
type WorkflowFile struct {
	Path string // repo-relative, e.g. .github/workflows/ci.yml
	Data []byte
}

// contentsEntry is the subset of the GitHub contents API we need. The
// listing form omits Content; the single-file form includes it base64-encoded.
type contentsEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

// maxRemoteWorkflowFiles caps how many workflow files we fetch from a remote
// repo (one API request each). Repos rarely have more; if one does, we lint
// the first N in listing order and say so via the returned truncated flag.
const maxRemoteWorkflowFiles = 60

// ListWorkflowFiles fetches .github/workflows/*.yml|yaml from a remote repo
// via the contents API: one listing request plus one request per file.
// A repo without that directory returns a *NotFoundError.
func (c *Client) ListWorkflowFiles(owner, repo string) (files []WorkflowFile, truncated bool, err error) {
	dir := fmt.Sprintf("/repos/%s/%s/contents/.github/workflows", owner, repo)
	var entries []contentsEntry
	if err := c.get(dir, nil, &entries); err != nil {
		return nil, false, err
	}
	for _, e := range entries {
		if e.Type != "file" || (!strings.HasSuffix(e.Name, ".yml") && !strings.HasSuffix(e.Name, ".yaml")) {
			continue
		}
		if len(files) >= maxRemoteWorkflowFiles {
			truncated = true
			break
		}
		var f contentsEntry
		if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, e.Path), nil, &f); err != nil {
			return nil, false, fmt.Errorf("fetching %s: %w", e.Path, err)
		}
		if f.Encoding != "base64" {
			// Files over ~1 MB come back with encoding "none"; a workflow
			// that large is pathological — skip it rather than fail the scan.
			continue
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
		if err != nil {
			return nil, false, fmt.Errorf("decoding %s: %w", e.Path, err)
		}
		files = append(files, WorkflowFile{Path: e.Path, Data: data})
	}
	return files, truncated, nil
}

// ListRootFileNames returns the names of the files at the repository root
// (one contents call). Used to detect package-manager lockfiles for remote
// --diff previews; directories are excluded.
func (c *Client) ListRootFileNames(owner, repo string) ([]string, error) {
	var entries []contentsEntry
	if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/", owner, repo), nil, &entries); err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type == "file" {
			names = append(names, e.Name)
		}
	}
	return names, nil
}

// treeResp is the subset of the git trees API we need.
type treeResp struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"tree"`
	Truncated bool `json:"truncated"`
}

// ListActionFiles fetches conventionally-placed action metadata files
// (action.yml / action.yaml — see lint.IsActionPath) from a remote repo:
// one recursive git-trees call to find them, one contents call per file.
// truncated is true when either the tree listing was cut off by GitHub or
// the file cap was hit. isAction filters paths; it is passed in (rather
// than imported) to keep this package free of a lint dependency.
func (c *Client) ListActionFiles(owner, repo string, isAction func(string) bool, maxFiles int) (files []WorkflowFile, truncated bool, err error) {
	var tr treeResp
	if err := c.get(fmt.Sprintf("/repos/%s/%s/git/trees/HEAD", owner, repo), url.Values{"recursive": {"1"}}, &tr); err != nil {
		return nil, false, err
	}
	truncated = tr.Truncated
	for _, e := range tr.Tree {
		if e.Type != "blob" || !isAction(e.Path) {
			continue
		}
		if len(files) >= maxFiles {
			truncated = true
			break
		}
		var f contentsEntry
		if err := c.get(fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, e.Path), nil, &f); err != nil {
			return nil, false, fmt.Errorf("fetching %s: %w", e.Path, err)
		}
		if f.Encoding != "base64" {
			continue // >1 MB manifest: pathological, skip
		}
		data, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
		if derr != nil {
			return nil, false, fmt.Errorf("decoding %s: %w", e.Path, derr)
		}
		files = append(files, WorkflowFile{Path: e.Path, Data: data})
	}
	return files, truncated, nil
}
