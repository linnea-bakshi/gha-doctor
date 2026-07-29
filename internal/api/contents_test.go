package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"
)

func TestListWorkflowFiles(t *testing.T) {
	ci := "name: CI\non: push\njobs: {}\n"
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/.github/workflows":
			fmt.Fprint(w, `[
				{"name":"ci.yml","path":".github/workflows/ci.yml","type":"file"},
				{"name":"README.md","path":".github/workflows/README.md","type":"file"},
				{"name":"scripts","path":".github/workflows/scripts","type":"dir"},
				{"name":"deploy.yaml","path":".github/workflows/deploy.yaml","type":"file"}
			]`)
		case "/repos/o/r/contents/.github/workflows/ci.yml":
			fmt.Fprintf(w, `{"path":".github/workflows/ci.yml","encoding":"base64","content":%q}`,
				base64.StdEncoding.EncodeToString([]byte(ci)))
		case "/repos/o/r/contents/.github/workflows/deploy.yaml":
			// Oversized files come back with encoding "none" — must be skipped.
			fmt.Fprint(w, `{"path":".github/workflows/deploy.yaml","encoding":"none","content":""}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	files, truncated, err := c.ListWorkflowFiles("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("truncated should be false")
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 (non-yml, dirs, and encoding-none skipped): %v", len(files), files)
	}
	if files[0].Path != ".github/workflows/ci.yml" || string(files[0].Data) != ci {
		t.Errorf("file = %q / %q", files[0].Path, files[0].Data)
	}
}

func TestListWorkflowFilesNotFound(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, _, err := c.ListWorkflowFiles("o", "r")
	if _, ok := err.(*NotFoundError); !ok {
		t.Fatalf("want *NotFoundError, got %v", err)
	}
}

func TestListWorkflowFilesBase64WithNewlines(t *testing.T) {
	// The contents API wraps base64 at 60 chars with literal newlines.
	raw := "name: Long\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n"
	enc := base64.StdEncoding.EncodeToString([]byte(raw))
	wrapped := ""
	for i := 0; i < len(enc); i += 60 {
		end := i + 60
		if end > len(enc) {
			end = len(enc)
		}
		wrapped += enc[i:end] + "\n"
	}
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/.github/workflows":
			fmt.Fprint(w, `[{"name":"ci.yml","path":".github/workflows/ci.yml","type":"file"}]`)
		default:
			fmt.Fprintf(w, `{"path":".github/workflows/ci.yml","encoding":"base64","content":%q}`, wrapped)
		}
	}))
	defer srv.Close()

	files, _, err := c.ListWorkflowFiles("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || string(files[0].Data) != raw {
		t.Fatalf("newline-wrapped base64 not decoded: %v", files)
	}
}
