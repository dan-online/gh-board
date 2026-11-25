package gh

import "testing"

func TestInRepoDropsOtherReposIssues(t *testing.T) {
	items := []Item{
		{Issue: Issue{Number: 1, HTMLURL: "https://github.com/o/r/issues/1"}},
		{Issue: Issue{Number: 1, HTMLURL: "https://github.com/o/other/issues/1"}},
		{Issue: Issue{Number: 2, HTMLURL: "https://github.com/o/r-fork/issues/2"}},
	}
	got := inRepo(items, "o/r")
	if len(got) != 1 || got[0].Issue.HTMLURL != "https://github.com/o/r/issues/1" {
		t.Fatalf("got %+v", got)
	}
}
