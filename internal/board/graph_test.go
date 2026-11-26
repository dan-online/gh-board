package board

import (
	"bytes"
	"slices"
	"testing"

	"github.com/dan-online/gh-board/internal/gh"
)

func ref(repo string, n int, state string) gh.Ref {
	return gh.Ref{Repo: repo, Number: n, Title: "t", State: state}
}

// testBoard: #1 parents #2 and #3 (sub-issue order 3, 2); #3 is blocked by
// open #2, closed #4 and another repo's open #9; #5 stands alone.
func testBoard() *board {
	nodes := []*Node{
		{Number: 1, Title: "Feature", State: "open", Fields: map[string]string{"Status": "Inbox"},
			SubIssues: []gh.Ref{ref("o/r", 3, "open"), ref("o/r", 2, "open")}},
		{Number: 2, Title: "First", State: "open", Fields: map[string]string{"Status": "In progress", "Priority": "Now"},
			Parent: &gh.Ref{Repo: "o/r", Number: 1}, Blocking: []gh.Ref{ref("o/r", 3, "open")}, OpenPRs: []int{40},
			Lock: &Lock{Branch: "2-first"}},
		{Number: 3, Title: "Second \"quoted\"", State: "open", Fields: map[string]string{"Status": "Ready"},
			Parent:    &gh.Ref{Repo: "o/r", Number: 1},
			BlockedBy: []gh.Ref{ref("o/r", 2, "open"), ref("o/r", 4, "closed"), ref("x/y", 9, "open")}},
		{Number: 5, Title: "Alone", State: "open", Fields: map[string]string{"Status": "Ready"}},
	}
	b := &board{status: map[int]string{}, nodes: nodes, byNum: map[int]*Node{}}
	for _, n := range nodes {
		b.byNum[n.Number] = n
		b.status[n.Number] = n.Fields["Status"]
	}
	return b
}

func TestTreeNestsSubIssuesAndNotesOpenBlockers(t *testing.T) {
	a := testApp(t)
	var out bytes.Buffer
	a.Out = &out
	b := testBoard()
	a.tree(b, b.nodes)
	want := `#1 Feature [Inbox]
├── #3 Second "quoted" [Ready] - blocked by #2, x/y#9
└── #2 First [In progress Priority=Now] - PR #40, lock 2-first
#5 Alone [Ready]
`
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestTreeStartsAtAChildWhoseParentIsNotShown(t *testing.T) {
	a := testApp(t)
	var out bytes.Buffer
	a.Out = &out
	b := testBoard()
	a.tree(b, []*Node{b.byNum[3]})
	if want := "#3 Second \"quoted\" [Ready] - blocked by #2, x/y#9\n"; out.String() != want {
		t.Fatalf("got %q", out.String())
	}
}

func TestComponentFollowsEveryRelationButNotOtherRepos(t *testing.T) {
	a := testApp(t)
	b := testBoard()
	b.byNum[9] = &Node{Number: 9} // same number as x/y#9, but not that issue
	b.nodes = append(b.nodes, b.byNum[9])
	var got []int
	for _, n := range a.component(b, b.byNum[2]) {
		got = append(got, n.Number)
	}
	if want := []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMermaidDrawsShownEdgesAndEscapesTitles(t *testing.T) {
	a := testApp(t)
	var out bytes.Buffer
	a.Out = &out
	b := testBoard()
	b.byNum[2].State = "closed"
	a.mermaid(b, b.nodes)
	want := `flowchart TD
  i1["#1 Feature<br/>Inbox"]
  i2["#2 First<br/>In progress Priority=Now"]
  i3["#3 Second #quot;quoted#quot;<br/>Ready"]
  i5["#5 Alone<br/>Ready"]
  i1 --> i3
  i1 --> i2
  i2 -. blocks .-> i3
  classDef closed stroke-dasharray: 4 4
  class i2 closed
`
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestRefLineNamesStatusOnlyForOpenBoardIssues(t *testing.T) {
	a := testApp(t)
	b := testBoard()
	cases := map[string]gh.Ref{
		"#2 t [In progress]": ref("o/r", 2, "open"),
		"#4 t (closed)":      ref("o/r", 4, "closed"),
		"x/y#2 t":            ref("x/y", 2, "open"),
	}
	for want, r := range cases {
		if got := a.refLine(b, r); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
