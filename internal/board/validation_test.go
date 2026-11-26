package board

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dan-online/gh-board/internal/config"
	"github.com/dan-online/gh-board/internal/gh"
	"github.com/dan-online/gh-board/internal/git"
)

func validationCheckout(t *testing.T) git.Git {
	t.Helper()
	g := git.Git{Dir: t.TempDir()}
	for _, args := range [][]string{{"init"}, {"remote", "add", "origin", "https://github.com/o/r"}} {
		if _, err := g.Run(args...); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestReadyRequiresFieldsWhenNoFormMatches(t *testing.T) {
	stubGitHub(t, func(r *http.Request) (int, string) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/o/r/issues/1":
			return 200, `{"number":1,"html_url":"https://github.com/o/r/issues/1","labels":[{"name":"unmatched"}]}`
		case "GET /users/o":
			return 200, `{"type":"User"}`
		case "GET /users/o/projectsV2/1/fields":
			return 200, `[{"id":1,"name":"Status","options":[{"id":"ready","name":{"raw":"Ready"}}]},{"id":2,"name":"Priority"}]`
		case "GET /users/o/projectsV2/1/items":
			return 200, `[{"id":1,"content_type":"Issue","content":{"number":1,"html_url":"https://github.com/o/r/issues/1"}}]`
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
			return 0, ""
		}
	})
	a := testApp(t)
	a.Cfg.Forms = []config.Form{{Label: "bug", File: "bug.yml"}}
	f := a.Cfg.Fields["Priority"]
	f.Required = true
	a.Cfg.Fields["Priority"] = f
	var err error
	if a.GH, err = gh.New(a.Cfg); err != nil {
		t.Fatal(err)
	}
	if err := a.Ready(1); err == nil || !strings.Contains(err.Error(), "Priority is not set") {
		t.Fatalf("got %v, want missing Priority error", err)
	}
}

func TestFileDoesNotCreateIssueWithStaleProjectOption(t *testing.T) {
	stubGitHub(t, func(r *http.Request) (int, string) {
		switch r.Method + " " + r.URL.Path {
		case "GET /users/o":
			return 200, `{"type":"User"}`
		case "GET /users/o/projectsV2/1/fields":
			return 200, `[{"id":1,"name":"Status","options":[{"id":"inbox","name":{"raw":"Inbox"}}]},
				{"id":2,"name":"Priority","options":[{"id":"next","name":{"raw":"Next"}}]}]`
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
			return 0, ""
		}
	})
	a := testApp(t)
	var err error
	if a.GH, err = gh.New(a.Cfg); err != nil {
		t.Fatal(err)
	}
	body := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(body, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = a.File(FileOpts{Type: "Task", Title: "Test", BodyFile: body, Fields: []string{"Priority=Now"}})
	if err == nil || !strings.Contains(err.Error(), `project field Priority has no option "Now"`) {
		t.Fatalf("got %v, want stale option error", err)
	}
}

func TestDoctorReportsFieldLookupFailureOnce(t *testing.T) {
	calls := 0
	stubGitHub(t, func(r *http.Request) (int, string) {
		switch r.Method + " " + r.URL.Path {
		case "GET /user":
			return 200, `{"login":"o"}`
		case "GET /users/o":
			return 200, `{"type":"User"}`
		case "POST /graphql":
			return 200, `{"data":{"owner":{"projectV2":{"viewerCanUpdate":true}}}}`
		case "GET /repos/o/r":
			return 200, `{"permissions":{"push":true}}`
		case "GET /users/o/projectsV2/1/fields":
			calls++
			return 403, `{"message":"fields forbidden"}`
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
			return 0, ""
		}
	})
	g := validationCheckout(t)
	// Keep the dry-run push local.
	if _, err := g.Run("remote", "set-url", "--push", "origin", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(g.Dir, config.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`repo: o/r
remote: origin
project: {owner: o, number: 1}
api_version: "2026-03-10"
worktrees: .worktrees
types: [Task]
statuses: {new: Inbox, ready: Ready, claimed: In progress}
fields:
  Priority: {options: [Now, Next]}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err := Doctor(g.Dir, "", func(format string, args ...any) { _, _ = fmt.Fprintf(&out, format, args...) })
	if reports := strings.Count(out.String(), "fields forbidden"); err == nil || calls != 1 || reports != 1 {
		t.Fatalf("doctor returned %v, made %d field requests and reported the failure %d time(s):\n%s", err, calls, reports, out.String())
	}
}
