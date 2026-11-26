package board

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/dan-online/gh-board/internal/gh"
)

const testProject = "/users/o/projectsV2/1"

func issueCard(number int, state string, archived bool) string {
	archivedAt := "null"
	if archived {
		archivedAt = `"2026-11-25T00:00:00Z"`
	}
	return fmt.Sprintf(`{"id":%[1]d,"archived_at":%[2]s,"content_type":"Issue",
		"content":{"number":%[1]d,"node_id":"I%[1]d","state":%[3]q,"html_url":"https://github.com/o/r/issues/%[1]d"}}`,
		number, archivedAt, state)
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubGitHub answers every GitHub request with respond.
func stubGitHub(t *testing.T, respond func(*http.Request) (int, string)) {
	t.Helper()
	t.Setenv("GH_HOST", "github.com")
	t.Setenv("GH_TOKEN", "x")
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		status, body := respond(r)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
}

func connectGitHub(t *testing.T, a *App) {
	t.Helper()
	var err error
	if a.GH, err = gh.New(a.Cfg); err != nil {
		t.Fatal(err)
	}
}

// fakeBoardAPI also answers the project owner and fields lookups.
func fakeBoardAPI(t *testing.T, a *App, respond func(*http.Request) (int, string)) {
	t.Helper()
	stubGitHub(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/users/o":
			return 200, `{"type":"User"}`
		case testProject + "/fields":
			return 200, `[{"id":1,"name":"Status","data_type":"single_select"}]`
		}
		return respond(r)
	})
	connectGitHub(t, a)
}

func TestItemScansOnlyAfterProjectSearchMiss(t *testing.T) {
	for _, tc := range []struct {
		name         string
		searchStatus int
		card         string
	}{
		{"archived", 200, issueCard(2, "open", true)},
		{"missing", 200, ""},
		{"forbidden", 403, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			var searches, scans int
			fakeBoardAPI(t, a, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/repos/o/r/issues/2":
					return 200, `{"number":2,"node_id":"I2","html_url":"https://github.com/o/r/issues/2"}`
				case testProject + "/items":
					if r.URL.Query().Get("q") != "" {
						searches++
						if tc.searchStatus == 403 {
							return 403, `{"message":"Forbidden"}`
						}
						return 200, "[]"
					}
					scans++
					return 200, "[" + tc.card + "]"
				default:
					t.Fatalf("unexpected request %s", r.URL)
					return 0, ""
				}
			})
			issue, card, err := a.item(2)
			switch tc.name {
			case "archived":
				if err != nil || issue == nil || issue.Number != 2 || card == nil || !card.Archived || card.ID != 2 || scans != 1 {
					t.Fatalf("issue %+v, card %+v, err %v after %d scans", issue, card, err, scans)
				}
			case "missing":
				if !errors.Is(err, gh.ErrNotOnProject) || scans != 1 {
					t.Fatalf("err %v after %d scans", err, scans)
				}
			case "forbidden":
				var httpErr *api.HTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != 403 || scans != 0 {
					t.Fatalf("err %v after %d scans", err, scans)
				}
			}
			if searches != 1 {
				t.Fatalf("searches %d, want 1", searches)
			}
		})
	}
}
