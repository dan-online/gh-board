package board

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCheckReportsMissingCardOrUnverifiedIssue(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"missing", 200, `{"state":"open"}`, "not on the board"},
		{"unknown", 503, `{"message":"issue lookup unavailable"}`, "issue state could not be verified"},
		{"closed", 200, `{"state":"closed"}`, "no drift"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := setup(t, nil)
			serveBoardAPI(t, c.a, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/users/o":
					return 200, `{"type":"User"}`
				case "/users/o/projectsV2/1/fields", "/users/o/projectsV2/1/items":
					return 200, `[]`
				case "/graphql":
					return 200, fmt.Sprintf(`{"data":{"repository":{"c0":{"oid":%q,"message":"claim\n\nBoard-Attempt: abc123\nBoard-Branch: 7-thing","committer":{"date":"2026-11-26T10:00:00Z"}}}}}`, c.lock.SHA)
				case "/repos/o/r/pulls":
					if head := r.URL.Query().Get("head"); head != "o:7-thing" {
						t.Fatalf("read PRs for %q", head)
					}
					return 200, `[{"number":11,"state":"closed","body":"<!-- board-attempt: old -->"},{"number":12,"state":"open","body":"<!-- board-attempt: abc123 -->"}]`
				case "/repos/o/r/issues/7":
					return tc.status, tc.body
				}
				t.Fatalf("unexpected request: %s", r.URL)
				return 0, ""
			})
			if err := c.a.Check(); err != nil {
				t.Fatal(err)
			}
			out := c.a.Out.(*bytes.Buffer).String()
			if !strings.Contains(out, tc.want) || tc.name != "closed" && (!strings.Contains(out, "PR #12 open") || strings.Contains(out, "no drift")) {
				t.Fatalf("unexpected check output: %s", out)
			}
		})
	}
}
