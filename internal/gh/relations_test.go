package gh

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/dan-online/gh-board/internal/config"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// fakeGraphQL answers each GraphQL request with the next reply and records the requests.
func fakeGraphQL(t *testing.T, replies ...string) (*Client, *[]gqlRequest) {
	t.Helper()
	var reqs []gqlRequest
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: "github.com", AuthToken: "x",
		Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			var req gqlRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			reqs = append(reqs, req)
			if len(reqs) > len(replies) {
				t.Fatalf("unexpected request %d", len(reqs))
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(replies[len(reqs)-1])), Request: r}, nil
		})})
	if err != nil {
		t.Fatal(err)
	}
	return &Client{gql: gql}, &reqs
}

const emptyPage = `{"pageInfo":{"hasNextPage":false},"nodes":[]}`

func TestRelationsReadsEveryPageOfALongList(t *testing.T) {
	c, reqs := fakeGraphQL(t,
		`{"data":{"nodes":[{"id":"I1","number":1,"parent":null,"subIssues":`+emptyPage+`,"blockedBy":`+emptyPage+`,
			"blocking":{"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[{"id":"I2","number":2,"state":"OPEN","repository":{"nameWithOwner":"o/r"}}]}}]}}`,
		`{"data":{"node":{"blocking":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":3,"state":"CLOSED","repository":{"nameWithOwner":"o/r"}}]}}}}`)
	rels, err := c.Relations([]string{"I1"})
	if err != nil {
		t.Fatal(err)
	}
	got := rels["I1"].Blocking
	if len(got) != 2 || got[0].Number != 2 || got[0].NodeID != "I2" || got[1].Number != 3 || got[1].State != "closed" {
		t.Fatalf("blocking %+v", got)
	}
	next := (*reqs)[1]
	if next.Variables["after"] != "c1" || next.Variables["id"] != "I1" {
		t.Fatalf("second page variables %v", next.Variables)
	}
	if !strings.Contains(next.Query, "blocking(first: 100, after: $after)") {
		t.Fatalf("second page query %s", next.Query)
	}
}

func TestRelationsRefusesAMissingIssue(t *testing.T) {
	c, _ := fakeGraphQL(t, `{"data":{"nodes":[null]}}`)
	if _, err := c.Relations([]string{"I1"}); err == nil || !strings.Contains(err.Error(), "I1") {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusesReadsOnlyThisProjectsStatus(t *testing.T) {
	c, _ := fakeGraphQL(t, `{"data":{"nodes":[
		{"id":"I1","projectItems":{"pageInfo":{"hasNextPage":false},"nodes":[
			{"project":{"number":2,"owner":{"login":"o"}},"status":{"name":"Elsewhere"}},
			{"project":{"number":1,"owner":{"login":"other"}},"status":{"name":"OtherOwner"}},
			{"project":{"number":1,"owner":{"login":"O"}},"status":{"name":"Ready"}}]}},
		{"id":"I2","projectItems":{"pageInfo":{"hasNextPage":false},"nodes":[
			{"project":{"number":1,"owner":{"login":"o"}},"status":null}]}}]}}`)
	c.cfg = &config.Config{}
	c.cfg.Project.Owner, c.cfg.Project.Number = "o", 1
	got, err := c.Statuses([]string{"I1", "I2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["I1"] != "Ready" {
		t.Fatalf("got %v", got)
	}
}
