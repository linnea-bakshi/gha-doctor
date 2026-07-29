package api

import (
	"encoding/base64"
	"fmt"
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
