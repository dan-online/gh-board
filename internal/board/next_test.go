package board

import (
	"slices"
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
	var got []int
	for _, c := range a.queue(items) {
		got = append(got, c.Number)
	}
	// Now before Next before unset; board order within each.
	if want := []int{3, 10, 1, 9, 2}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
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
