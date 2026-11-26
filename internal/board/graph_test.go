package board

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
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
	b := &board{nodes: nodes, byNum: map[int]*Node{}}
	for _, n := range nodes {
		b.byNum[n.Number] = n
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

func graphQLIDs(t *testing.T, r *http.Request) []string {
	t.Helper()
	var req struct {
		Variables struct{ IDs []string }
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatal(err)
	}
	return req.Variables.IDs
}

func TestComponentFollowsEveryRelationButNotOtherRepos(t *testing.T) {
	cards := "[" + strings.Join([]string{
		issueCard(6, "open", false), issueCard(5, "open", false), issueCard(2, "open", false),
		issueCard(7, "open", false), issueCard(1, "open", false), issueCard(9, "open", false),
		issueCard(3, "open", false), issueCard(4, "closed", false), issueCard(8, "open", true),
	}, ",") + "]"
	// Only the closed blocker leads to #6. #5 is reachable only via blocking.
	relations := map[string]string{
		"I1": `{"id":"I1","parent":{"number":2,"repository":{"nameWithOwner":"o/r"}},
			"subIssues":{"nodes":[{"number":3,"repository":{"nameWithOwner":"o/r"}},
				{"number":8,"repository":{"nameWithOwner":"o/r"}},{"number":9,"repository":{"nameWithOwner":"x/y"}}]},
			"blockedBy":{"nodes":[{"number":4,"state":"CLOSED","repository":{"nameWithOwner":"o/r"}}]},
			"blocking":{"nodes":[{"number":5,"repository":{"nameWithOwner":"O/R"}}]}}`,
		"I2": `{"id":"I2","subIssues":{"nodes":[{"number":1,"repository":{"nameWithOwner":"o/r"}}]}}`,
		"I3": `{"id":"I3"}`,
		"I4": `{"id":"I4","subIssues":{"nodes":[{"number":6,"repository":{"nameWithOwner":"o/r"}}]}}`,
		"I5": `{"id":"I5"}`,
		"I6": `{"id":"I6"}`,
	}
	c, _ := setup(t, nil) // leaves #7 with an unrelated lock
	sha := run(t, c.a.Git.Dir, "rev-parse", "HEAD")
	run(t, c.a.Git.Dir, "push", "-q", "origin", "HEAD:refs/claims/1")
	for _, tc := range []struct {
		name    string
		closed  bool
		want    []int
		batches [][]string
	}{
		{"open", false, []int{5, 2, 1, 3}, [][]string{{"I1"}, {"I2", "I3", "I5"}}},
		{"closed", true, []int{6, 5, 2, 1, 3, 4}, [][]string{{"I1"}, {"I2", "I3", "I4", "I5"}, {"I6"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.Git = c.a.Git
			var out bytes.Buffer
			a.Out = &out
			var batches [][]string
			var scans, locks int
			fakeBoardAPI(t, a, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case testProject + "/items":
					scans++
					return 200, cards
				case "/graphql":
					ids := graphQLIDs(t, r)
					slices.Sort(ids)
					batches = append(batches, ids)
					var nodes []string
					for _, id := range ids {
						node, ok := relations[id]
						if !ok {
							t.Fatalf("relations requested for excluded issue %s", id)
						}
						nodes = append(nodes, node)
					}
					return 200, `{"data":{"nodes":[` + strings.Join(nodes, ",") + `]}}`
				case "/repos/o/r/git/commits/" + sha:
					locks++
					return 200, `{"message":"claim\n\nBoard-Branch: 1-root\n"}`
				default:
					t.Fatalf("unexpected request %s", r.URL)
					return 0, ""
				}
			})
			if err := a.Graph(GraphOpts{Issue: 1, Closed: tc.closed, JSON: true}); err != nil {
				t.Fatal(err)
			}
			var nodes []Node
			if err := json.Unmarshal(out.Bytes(), &nodes); err != nil {
				t.Fatal(err)
			}
			var got []int
			for _, n := range nodes {
				got = append(got, n.Number)
				if n.Number == 1 && (n.Lock == nil || n.Lock.Branch != "1-root") {
					t.Fatalf("root lock = %+v", n.Lock)
				}
			}
			if !slices.Equal(got, tc.want) || !slices.EqualFunc(batches, tc.batches, slices.Equal[[]string]) || scans != 1 || locks != 1 {
				t.Fatalf("nodes %v, batches %v, scans %d, lock reads %d; want nodes %v, batches %v, one scan and one lock read",
					got, batches, scans, locks, tc.want, tc.batches)
			}
		})
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

func TestRelatedStatusesRequestsEachOpenLocalIssueOnce(t *testing.T) {
	a := testApp(t)
	local := gh.Ref{Number: 2, NodeID: "I2", Repo: "o/r", State: "open"}
	n := &Node{
		Parent: &local, BlockedBy: []gh.Ref{local},
		SubIssues: []gh.Ref{{Number: 3, NodeID: "I3", Repo: "o/r", State: "closed"}},
		Blocking:  []gh.Ref{{Number: 2, NodeID: "X2", Repo: "x/y", State: "open"}},
	}
	calls := 0
	fakeBoardAPI(t, a, func(r *http.Request) (int, string) {
		calls++
		ids := graphQLIDs(t, r)
		if r.URL.Path != "/graphql" || !slices.Equal(ids, []string{"I2"}) {
			t.Fatalf("unexpected request %s with IDs %v", r.URL, ids)
		}
		return 200, `{"data":{"nodes":[{"id":"I2","projectItems":{"nodes":[
			{"project":{"number":1,"owner":{"login":"o"}},"status":{"name":"Ready"}}]}}]}}`
	})
	statuses, err := a.relatedStatuses(n)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(statuses) != 1 || statuses[2] != "Ready" {
		t.Fatalf("statuses %v after %d requests", statuses, calls)
	}
}

func TestRefLineNamesStatusOnlyForOpenBoardIssues(t *testing.T) {
	a := testApp(t)
	statuses := map[int]string{2: "In progress", 4: "Ready"}
	cases := map[string]gh.Ref{
		"#2 t [In progress]": ref("o/r", 2, "open"),
		"#4 t (closed)":      ref("o/r", 4, "closed"),
		"x/y#2 t":            ref("x/y", 2, "open"),
	}
	for want, r := range cases {
		if got := a.refLine(statuses, r); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
