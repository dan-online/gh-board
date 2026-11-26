package gh

import (
	"slices"
	"strings"
	"testing"

	"github.com/dan-online/gh-board/internal/config"
)

func TestInRepoDropsOtherReposIssues(t *testing.T) {
	items := []Item{
		{Issue: Issue{Number: 1, HTMLURL: "https://github.com/o/r/issues/1"}},
		{Issue: Issue{Number: 1, HTMLURL: "https://github.com/o/other/issues/1"}},
		{Issue: Issue{Number: 2, HTMLURL: "https://github.com/o/r-fork/issues/2"}},
		{Issue: Issue{Number: 3, HTMLURL: "https://github.com/O/R/issues/3"}},
	}
	got := inRepo(items, "o/r")
	if len(got) != 2 || got[0].Issue.Number != 1 || got[1].Issue.Number != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestRepoInfoReadsAllLinkedProjectPages(t *testing.T) {
	c, reqs := fakeGraphQL(t,
		`{"data":{"repository":{"id":"r","owner":{"login":"o"},"projectsV2":{"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[{"number":1,"owner":{"login":"o"}},{"number":2,"closed":true}]},"issueTypes":{"nodes":[{"name":"Task","isEnabled":true}]}}}}`,
		`{"data":{"repository":{"id":"r","owner":{"login":"o"},"projectsV2":{"pageInfo":{"hasNextPage":true,"endCursor":"c2"},"nodes":[{"number":3,"closed":true}]}}}}`,
		`{"data":{"repository":{"id":"r","owner":{"login":"o"},"projectsV2":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":4,"owner":{"login":"other"}}]}}}}`)
	c.cfg = &config.Config{Repo: "o/r"}
	info, err := c.RepoInfo()
	if err != nil {
		t.Fatal(err)
	}
	want := []ProjectRef{{Owner: "o", Number: 1}, {Owner: "other", Number: 4}}
	if !slices.Equal(info.Projects, want) {
		t.Fatalf("got projects %v, want %v", info.Projects, want)
	}
	if !slices.Equal(info.IssueTypes, []string{"Task"}) {
		t.Fatalf("got issue types %v, want Task", info.IssueTypes)
	}
	if !strings.Contains((*reqs)[0].Query, "pageInfo { hasNextPage endCursor }") {
		t.Fatalf("first request did not query pagination data: %s", (*reqs)[0].Query)
	}
	for i, cursor := range []string{"c1", "c2"} {
		next := (*reqs)[i+1]
		if next.Variables["after"] != cursor || !strings.Contains(next.Query, "after: $after") {
			t.Fatalf("page %d did not use cursor %q: %+v", i+2, cursor, next)
		}
	}
}
