package gh

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/dan-online/gh-board/internal/config"
)

func fakeREST(t *testing.T, request roundTrip) *api.RESTClient {
	t.Helper()
	c, err := api.NewRESTClient(api.ClientOptions{Host: "github.com", AuthToken: "x", Transport: request})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCommitsBatchesAndFallsBackForNullObjects(t *testing.T) {
	shas := make([]string, 52)
	for i := range shas {
		shas[i] = fmt.Sprintf("%040x", i+1)
	}
	const date = "2026-11-26T10:00:00Z"
	var replies []string
	for batch := range slices.Chunk(shas, 50) {
		repo := map[string]any{}
		for i, sha := range batch {
			if sha == shas[1] || sha == shas[51] {
				repo[fmt.Sprintf("c%d", i)] = nil
				continue
			}
			repo[fmt.Sprintf("c%d", i)] = map[string]any{"oid": sha, "message": sha, "committer": map[string]string{"date": date}}
		}
		body, err := json.Marshal(map[string]any{"data": map[string]any{"repository": repo}})
		if err != nil {
			t.Fatal(err)
		}
		replies = append(replies, string(body))
	}
	c, reqs := fakeGraphQL(t, replies...)
	c.cfg = &config.Config{Repo: "o/r"}
	var restReads []string
	c.rest = fakeREST(t, func(r *http.Request) (*http.Response, error) {
		sha, ok := strings.CutPrefix(r.URL.Path, "/repos/o/r/git/commits/")
		if !ok || r.Method != http.MethodGet {
			t.Fatalf("unexpected REST request: %s %s", r.Method, r.URL)
		}
		restReads = append(restReads, sha)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Request: r}
		body := fmt.Sprintf(`{"sha":%q,"message":%q,"committer":{"date":%q}}`, sha, sha, date)
		if sha == shas[51] {
			resp.StatusCode = http.StatusNotFound
			body = `{"message":"Not found"}`
		}
		resp.Body = io.NopCloser(strings.NewReader(body))
		return resp, nil
	})
	got, err := c.Commits(shas)
	if err != nil {
		t.Fatal(err)
	}
	if len(*reqs) != 2 || !slices.Equal(restReads, []string{shas[1], shas[51]}) {
		t.Fatalf("GraphQL calls %d, REST reads %v", len(*reqs), restReads)
	}
	for i, batch := range slices.Collect(slices.Chunk(shas, 50)) {
		q := (*reqs)[i].Query
		if strings.Count(q, "object(oid:") != len(batch) {
			t.Fatalf("wrong number of requested objects in batch %d", i)
		}
		for j, sha := range batch {
			if !strings.Contains(q, fmt.Sprintf("c%d: object(oid: %q)", j, sha)) {
				t.Fatalf("%s not requested in alias c%d of batch %d", sha, j, i)
			}
		}
	}
	for _, sha := range shas[:51] {
		r := got[sha]
		if r.Err != nil || r.Commit == nil || r.Commit.SHA != sha || r.Commit.Message != sha || r.Commit.Committer.Date.Format(time.RFC3339) != date {
			t.Fatalf("%s: %+v, %v", sha, r.Commit, r.Err)
		}
	}
	if err := got[shas[51]].Err; !NotFound(err) {
		t.Fatalf("missing commit error = %v", err)
	}
}
