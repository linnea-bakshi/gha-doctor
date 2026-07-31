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
		case "/repos/o/r/contents/.github":
			// Still listed once: the doctor-config search needs it even
			// after renovate answered the D017 question at the root.
			fmt.Fprint(w, `[]`)
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

func TestFindRepoMetaDoctorConfigAtRootStopsSearch(t *testing.T) {
	requests := 0
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			fmt.Fprint(w, `[
				{"name":".gha-doctor.yml","path":".gha-doctor.yml","type":"file"},
				{"name":"renovate.json","path":"renovate.json","type":"file"},
				{"name":"go.sum","path":"go.sum","type":"file"},
				{"name":".github","path":".github","type":"dir"}
			]`)
		case "/repos/o/r/contents/.gha-doctor.yml":
			body := base64.StdEncoding.EncodeToString([]byte("disable: [D004]\n"))
			fmt.Fprintf(w, `{"encoding":"base64","content":%q}`, body)
		default:
			t.Errorf("unexpected request: %s (root answered everything)", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m, err := c.FindRepoMeta("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Errorf("expected 2 requests (root listing + config fetch), got %d", requests)
	}
	if m.DoctorConfig == nil || m.DoctorConfig.Path != ".gha-doctor.yml" {
		t.Fatalf("doctor config = %+v", m.DoctorConfig)
	}
	if string(m.DoctorConfig.Data) != "disable: [D004]\n" {
		t.Errorf("config data = %q", m.DoctorConfig.Data)
	}
	if m.RenovatePath != "renovate.json" || m.Dependabot != nil {
		t.Errorf("renovate=%q dependabot=%+v", m.RenovatePath, m.Dependabot)
	}
	want := []string{".gha-doctor.yml", "renovate.json", "go.sum"}
	if fmt.Sprint(m.RootFiles) != fmt.Sprint(want) {
		t.Errorf("root files = %v", m.RootFiles)
	}
}

func TestFindRepoMetaDoctorConfigInGithubDir(t *testing.T) {
	c, srv := testClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			fmt.Fprint(w, `[{"name":".github","path":".github","type":"dir"}]`)
		case "/repos/o/r/contents/.github":
			fmt.Fprint(w, `[
				{"name":"gha-doctor.yml","path":".github/gha-doctor.yml","type":"file"},
				{"name":"dependabot.yml","path":".github/dependabot.yml","type":"file"}
			]`)
		case "/repos/o/r/contents/.github/gha-doctor.yml":
			body := base64.StdEncoding.EncodeToString([]byte("runs: 150\n"))
			fmt.Fprintf(w, `{"encoding":"base64","content":%q}`, body)
		case "/repos/o/r/contents/.github/dependabot.yml":
			body := base64.StdEncoding.EncodeToString([]byte("updates: []\n"))
			fmt.Fprintf(w, `{"encoding":"base64","content":%q}`, body)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m, err := c.FindRepoMeta("o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if m.DoctorConfig == nil || m.DoctorConfig.Path != ".github/gha-doctor.yml" || string(m.DoctorConfig.Data) != "runs: 150\n" {
		t.Fatalf("doctor config = %+v", m.DoctorConfig)
	}
	if m.Dependabot == nil || m.Dependabot.Path != ".github/dependabot.yml" {
		t.Fatalf("dependabot = %+v", m.Dependabot)
	}
}
