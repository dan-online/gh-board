package board

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dan-online/gh-board/internal/config"
	"github.com/dan-online/gh-board/internal/gh"
)

func testApp(t *testing.T) *App {
	t.Helper()
	cfg, err := config.Parse([]byte(`
repo: o/r
remote: origin
project: { owner: o, number: 1 }
api_version: "2026-03-10"
worktrees: .worktrees
types: [Task]
statuses: { new: Inbox, ready: Ready, claimed: In progress }
fields:
  Priority: { options: [Now, Next] }
queue: { sort: Priority }
`))
	if err != nil {
		t.Fatal(err)
	}
	return &App{Cfg: cfg}
}

func item(number int, status, priority string) gh.Item {
	it := gh.Item{Values: map[string]string{"Status": status}}
	if priority != "" {
		it.Values["Priority"] = priority
	}
	it.Issue.Number, it.Issue.State = number, "open"
	return it
}

func TestQueue(t *testing.T) {
	a := testApp(t)
	blocked := item(5, "Ready", "Now")
	blocked.Issue.Dependencies.BlockedBy = 1
	closed := item(6, "Ready", "Now")
	closed.Issue.State = "closed"
	archived := item(7, "Ready", "Now")
	archived.Archived = true
	linked := item(8, "Ready", "Now")
	linked.OpenPRs = []int{40}
	items := []gh.Item{
		item(1, "Ready", "Next"),
		item(2, "Ready", ""),
		item(3, "Ready", "Now"),
		item(4, "Inbox", "Now"),
		blocked, closed, archived, linked,
		item(9, "Ready", "Next"),
		item(10, "Ready", "Now"),
	}
	var got, marked []int
	for _, c := range a.queue(items) {
		got = append(got, c.Number)
		if c.blocked {
			marked = append(marked, c.Number)
		}
	}
	// Now before Next before unset; board order within each. Blocked issues stay, marked.
	if want := []int{3, 5, 10, 1, 9, 2}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if !slices.Equal(marked, []int{5}) {
		t.Fatalf("marked blocked %v", marked)
	}
}

func TestQueueWithoutSortKeepsBoardOrder(t *testing.T) {
	a := testApp(t)
	a.Cfg.Queue = nil
	var got []int
	for _, c := range a.queue([]gh.Item{item(3, "Ready", "Next"), item(1, "Ready", "Now")}) {
		got = append(got, c.Number)
	}
	if want := []int{3, 1}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func serveBoardAPI(t *testing.T, a *App, reply func(*http.Request) (int, string)) {
	t.Helper()
	stubGitHub(t, reply)
	a.Cfg = testApp(t).Cfg
	connectGitHub(t, a)
}

func TestNextStopsAtLimitAndReusesStackParent(t *testing.T) {
	for _, limit := range []int{0, 2} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			c, _ := setup(t, nil)
			lateSHA := run(t, c.a.Git.Dir, "rev-parse", "HEAD")
			run(t, c.a.Git.Dir, "push", "origin", lateSHA+":"+lockRef(1))
			parentCommit := "/repos/o/r/git/commits/" + c.lock.SHA
			lateCommit := "/repos/o/r/git/commits/" + lateSHA
			var items []string
			for _, card := range []struct {
				n, blockers int
				priority    string
			}{
				{1, 1, "Next"}, {2, 1, "Now"}, {6, 1, "Now"}, {3, 1, "Now"}, {4, 1, "Now"}, {5, 0, "Next"},
			} {
				items = append(items, fmt.Sprintf(`{"content_type":"Issue","content":{"number":%d,"state":"open","html_url":"https://github.com/o/r/issues/%d","issue_dependencies_summary":{"blocked_by":%d}},
					"fields":[{"name":"Status","data_type":"single_select","value":{"name":{"raw":"Ready"}}},{"name":"Priority","data_type":"single_select","value":{"name":{"raw":%q}}}]}`,
					card.n, card.n, card.blockers, card.priority))
			}
			reads := map[string]int{}
			serveBoardAPI(t, c.a, func(r *http.Request) (int, string) {
				reads[r.URL.Path]++
				switch r.URL.Path {
				case "/users/o":
					return 200, `{"type":"User"}`
				case "/users/o/projectsV2/1/fields":
					return 200, `[{"id":1,"name":"Status"},{"id":2,"name":"Priority"}]`
				case "/users/o/projectsV2/1/items":
					return 200, "[" + strings.Join(items, ",") + "]"
				case "/repos/o/r/issues/2/dependencies/blocked_by":
					return 200, `[{"number":7,"state":"open","html_url":"https://github.com/other/r/issues/7"}]`
				case "/repos/o/r/issues/3/dependencies/blocked_by", "/repos/o/r/issues/4/dependencies/blocked_by":
					return 200, `[{"number":7,"state":"open","html_url":"https://github.com/O/R/issues/7"}]`
				case "/repos/o/r/issues/6/dependencies/blocked_by":
					return 200, `[{"number":9,"state":"open","html_url":"https://github.com/o/r/issues/9"}]`
				case "/repos/o/r/issues/1/dependencies/blocked_by":
					return 200, `[]`
				case parentCommit:
					return 200, `{"message":"claim\n\nBoard-Attempt: abc123\nBoard-Branch: 7-thing"}`
				case lateCommit:
					return 200, `{"message":"claim\n\nBoard-Attempt: late\nBoard-Branch: 1-late"}`
				case "/repos/o/r/pulls":
					if r.URL.Query().Get("head") == "o:7-thing" {
						return 200, `[{"number":12,"state":"open","body":"<!-- board-attempt: abc123 -->"}]`
					}
					return 200, `[{"number":13,"state":"closed","body":"<!-- board-attempt: late -->"}]`
				case "/repos/o/r/stacks":
					return 200, `[]`
				}
				t.Fatalf("unexpected request: %s", r.URL)
				return 0, ""
			})
			if err := c.a.Next(limit, true); err != nil {
				t.Fatal(err)
			}
			var got []Candidate
			if err := json.Unmarshal(c.a.Out.(*bytes.Buffer).Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var nums, parents []int
			for _, candidate := range got {
				nums, parents = append(nums, candidate.Number), append(parents, candidate.StackOn)
			}
			wantNums, wantParents, late := []int{3, 4}, []int{7, 7}, 0
			if limit == 0 {
				wantNums, wantParents, late = append(wantNums, 5), append(wantParents, 0), 1
			}
			if !slices.Equal(nums, wantNums) || !slices.Equal(parents, wantParents) {
				t.Fatalf("issues %v, stack parents %v", nums, parents)
			}
			if reads[parentCommit] != 1 || reads["/repos/o/r/pulls"] != 1+late || reads["/repos/o/r/stacks"] != 1 || reads[lateCommit] != late || reads["/repos/o/r/issues/1/dependencies/blocked_by"] != late {
				t.Fatalf("unexpected reads: %v", reads)
			}
		})
	}
}

func TestNextReportsFailedStackLookup(t *testing.T) {
	c, _ := setup(t, nil)
	serveBoardAPI(t, c.a, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/repos/o/r/issues/8/dependencies/blocked_by":
			return 200, `[{"number":7,"state":"open","html_url":"https://github.com/o/r/issues/7"}]`
		case "/repos/o/r/git/commits/" + c.lock.SHA:
			return 503, `{"message":"commit lookup unavailable"}`
		}
		t.Fatalf("unexpected request: %s", r.URL)
		return 0, ""
	})
	if _, err := c.a.claimable([]Candidate{{Number: 8, blocked: true}}, 1); err == nil || !strings.Contains(err.Error(), "commit lookup unavailable") {
		t.Fatalf("got %v", err)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Point agent docs, forms and scripts/board at uptempohq/uptempo": "point-agent-docs-forms-and-scripts-board-at-uptempohq-uptempo",
		"  --Fix: the (bad) thing!  ":                                    "fix-the-bad-thing",
		"Ünïcode tïtle":                                                  "n-code-t-tle",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
