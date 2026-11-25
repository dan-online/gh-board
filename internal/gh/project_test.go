package gh

import "testing"

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
