package gh

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dan-online/gh-board/internal/config"
)

func TestHasOpenPRsOnBaseChecksOnlyOnePage(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"open", `[{"number":12}]`, true},
		{"none", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := &Client{cfg: &config.Config{Repo: "o/r"}, rest: fakeREST(t, func(r *http.Request) (*http.Response, error) {
				calls++
				q := r.URL.Query()
				if calls != 1 || r.Method != http.MethodGet || r.URL.Path != "/repos/o/r/pulls" || q.Get("base") != "topic/a" || q.Get("state") != "open" || q.Get("per_page") != "1" {
					t.Fatalf("unexpected request %d: %s %s", calls, r.Method, r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Request: r,
					Header: http.Header{"Link": {`<https://api.github.com/repos/o/r/pulls?page=2>; rel="next"`}},
					Body:   io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			if got, err := c.HasOpenPRsOnBase("topic/a"); err != nil || got != tc.want || calls != 1 {
				t.Fatalf("got %v, %v after %d calls", got, err, calls)
			}
		})
	}
}
