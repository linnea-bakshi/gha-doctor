package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"
)

func TestFindUpdateConfigRenovateAtRoot(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			fmt.Fprint(w, `[
				{"name":"renovate.json","path":"renovate.json","type":"file"},
				{"name":".github","path":".github","type":"dir"}
			]`)
		default:
			t.Errorf("unexpected request: %s (renovate at root should stop the search)", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db, ren, err := c.FindUpdateConfig("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if db != nil || ren != "renovate.json" {
		t.Fatalf("got db=%+v ren=%q", db, ren)
	}
}

func TestFindUpdateConfigDependabotFetched(t *testing.T) {
	cfg := "version: 2\nupdates:\n  - package-ecosystem: github-actions\n    directory: /\n"
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			fmt.Fprint(w, `[
				{"name":"README.md","path":"README.md","type":"file"},
				{"name":".github","path":".github","type":"dir"}
			]`)
		case "/repos/o/r/contents/.github":
			fmt.Fprint(w, `[
				{"name":"workflows","path":".github/workflows","type":"dir"},
				{"name":"dependabot.yml","path":".github/dependabot.yml","type":"file"}
			]`)
		case "/repos/o/r/contents/.github/dependabot.yml":
			fmt.Fprintf(w, `{"path":".github/dependabot.yml","encoding":"base64","content":%q}`,
				base64.StdEncoding.EncodeToString([]byte(cfg)))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db, ren, err := c.FindUpdateConfig("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if ren != "" || db == nil || db.Path != ".github/dependabot.yml" || string(db.Data) != cfg {
		t.Fatalf("got db=%+v ren=%q", db, ren)
	}
}

func TestFindUpdateConfigRenovateInGithubDirWinsOverDependabot(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			fmt.Fprint(w, `[{"name":".github","path":".github","type":"dir"}]`)
		case "/repos/o/r/contents/.github":
			fmt.Fprint(w, `[
				{"name":"dependabot.yml","path":".github/dependabot.yml","type":"file"},
				{"name":"renovate.json5","path":".github/renovate.json5","type":"file"}
			]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db, ren, err := c.FindUpdateConfig("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if db != nil || ren != ".github/renovate.json5" {
		t.Fatalf("got db=%+v ren=%q", db, ren)
	}
}

func TestFindUpdateConfigNothingThere(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			fmt.Fprint(w, `[{"name":"main.go","path":"main.go","type":"file"}]`)
		default:
			t.Errorf("unexpected request: %s (no .github dir means no more requests)", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	db, ren, err := c.FindUpdateConfig("o", "r")
	if err != nil || db != nil || ren != "" {
		t.Fatalf("got db=%+v ren=%q err=%v", db, ren, err)
	}
}
