package board

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dan-online/gh-board/internal/gh"
)

// Node is one issue as graph --json and show --json print it.
type Node struct {
	Number    int               `json:"number"`
	Title     string            `json:"title"`
	State     string            `json:"state"`
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields"` // Status and the configured fields
	Labels    []string          `json:"labels"`
	Parent    *gh.Ref           `json:"parent"`
	SubIssues []gh.Ref          `json:"sub_issues"`
	BlockedBy []gh.Ref          `json:"blocked_by"`
	Blocking  []gh.Ref          `json:"blocking"`
	OpenPRs   []int             `json:"open_prs"`
	Lock      *Lock             `json:"lock"`
}

// Lock is the claim lock refs/claims/N, which stays until another claim takes it over.
type Lock struct {
	SHA    string    `json:"sha"`
	Branch string    `json:"branch"`
	Host   string    `json:"host"`
	Since  time.Time `json:"since"`
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// board is the project's issues in this repo, with the relations and locks of some of them.
type board struct {
	status map[int]string // every board issue's Status, for naming relation targets
	nodes  []*Node        // the loaded issues, in board order
	byNum  map[int]*Node
}

// load reads the board and the relations and locks of the items pick keeps.
func (a *App) load(pick func(*gh.Item) bool) (*board, error) {
	items, err := a.GH.Items()
	if err != nil {
		return nil, err
	}
	b := &board{status: map[int]string{}, byNum: map[int]*Node{}}
	var picked []*gh.Item
	var ids []string
	for i := range items {
		it := &items[i]
		b.status[it.Issue.Number] = it.Values["Status"]
		if pick(it) {
			picked = append(picked, it)
			ids = append(ids, it.Issue.NodeID)
		}
	}
	rels, err := a.GH.Relations(ids)
	if err != nil {
		return nil, err
	}
	locks, err := a.remoteRefs("refs/claims/*")
	if err != nil {
		return nil, err
	}
	for _, it := range picked {
		r := rels[it.Issue.NodeID]
		n := &Node{Number: it.Issue.Number, Title: it.Issue.Title, State: it.Issue.State, URL: it.Issue.HTMLURL,
			Fields: it.Values, Labels: orEmpty(it.Issue.LabelNames()), Parent: r.Parent, SubIssues: orEmpty(r.SubIssues),
			BlockedBy: orEmpty(r.BlockedBy), Blocking: orEmpty(r.Blocking), OpenPRs: orEmpty(it.OpenPRs)}
		ref := lockRef(n.Number)
		if sha, ok := locks[ref]; ok {
			l, err := a.describeLock(ref, sha)
			if err != nil {
				return nil, err
			}
			n.Lock = &Lock{SHA: l.SHA, Branch: l.Branch, Host: l.Host, Since: l.Time}
		}
		b.nodes = append(b.nodes, n)
		b.byNum[n.Number] = n
	}
	return b, nil
}

// local returns the loaded node a relation points at, or nil when it is
// in another repo or was not loaded.
func (a *App) local(b *board, r gh.Ref) *Node {
	if !strings.EqualFold(r.Repo, a.Cfg.Repo) {
		return nil
	}
	return b.byNum[r.Number]
}

// component is the loaded nodes connected to n by any relation, in board order.
func (a *App) component(b *board, n *Node) []*Node {
	seen := map[int]bool{n.Number: true}
	queue := []*Node{n}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visit := func(r gh.Ref) {
			if m := a.local(b, r); m != nil && !seen[m.Number] {
				seen[m.Number] = true
				queue = append(queue, m)
			}
		}
		for _, refs := range [][]gh.Ref{cur.SubIssues, cur.BlockedBy, cur.Blocking} {
			for _, r := range refs {
				visit(r)
			}
		}
		if cur.Parent != nil {
			visit(*cur.Parent)
		}
	}
	var out []*Node
	for _, m := range b.nodes {
		if seen[m.Number] {
			out = append(out, m)
		}
	}
	return out
}

type GraphOpts struct {
	Issue   int // 0 for the whole board
	Closed  bool
	JSON    bool
	Mermaid bool
}

// Graph prints the board's issues as a parent tree with their blockers.
func (a *App) Graph(o GraphOpts) error {
	b, err := a.load(func(it *gh.Item) bool { return !it.Archived && (o.Closed || it.Issue.State == "open") })
	if err != nil {
		return err
	}
	nodes := b.nodes
	if o.Issue != 0 {
		n := b.byNum[o.Issue]
		if n == nil {
			hint := ""
			if !o.Closed {
				hint = "; pass --closed to include closed issues"
			}
			return fmt.Errorf("issue #%d is not an open, unarchived issue on the board%s", o.Issue, hint)
		}
		nodes = a.component(b, n)
	}
	switch {
	case o.JSON:
		return writeJSON(a.Out, orEmpty(nodes))
	case o.Mermaid:
		a.mermaid(b, nodes)
	default:
		a.tree(b, nodes)
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// refName is #N for this repo's issues and owner/repo#N for others.
func (a *App) refName(r gh.Ref) string {
	if strings.EqualFold(r.Repo, a.Cfg.Repo) {
		return fmt.Sprintf("#%d", r.Number)
	}
	return fmt.Sprintf("%s#%d", r.Repo, r.Number)
}

// fieldText is "Status Size=S Priority=Next", leaving out unset fields.
func (a *App) fieldText(values map[string]string) string {
	parts := []string{values["Status"]}
	for _, name := range a.Cfg.FieldNames() {
		if v := values[name]; v != "" {
			parts = append(parts, name+"="+v)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// line is one issue's row in the tree: number, title, fields and what holds it up.
func (a *App) line(n *Node) string {
	s := fmt.Sprintf("#%d %s [%s]", n.Number, n.Title, a.fieldText(n.Fields))
	if n.State == "closed" {
		s += " closed"
	}
	var notes, open []string
	for _, r := range n.BlockedBy {
		if r.State == "open" {
			open = append(open, a.refName(r))
		}
	}
	if len(open) > 0 {
		notes = append(notes, "blocked by "+strings.Join(open, ", "))
	}
	for _, pr := range n.OpenPRs {
		notes = append(notes, fmt.Sprintf("PR #%d", pr))
	}
	if n.Lock != nil {
		notes = append(notes, "lock "+n.Lock.Branch)
	}
	if len(notes) > 0 {
		s += " - " + strings.Join(notes, ", ")
	}
	return s
}

// tree prints each root, an issue whose parent is not shown, with its shown
// sub-issues below it in sub-issue order.
func (a *App) tree(b *board, nodes []*Node) {
	shown := map[int]bool{}
	for _, n := range nodes {
		shown[n.Number] = true
	}
	children := func(n *Node) []*Node {
		var out []*Node
		for _, r := range n.SubIssues {
			if m := a.local(b, r); m != nil && shown[m.Number] {
				out = append(out, m)
			}
		}
		return out
	}
	var walk func(n *Node, prefix string)
	walk = func(n *Node, prefix string) {
		kids := children(n)
		for i, k := range kids {
			branch, next := "├── ", "│   "
			if i == len(kids)-1 {
				branch, next = "└── ", "    "
			}
			a.printf("%s%s%s\n", prefix, branch, a.line(k))
			walk(k, prefix+next)
		}
	}
	for _, n := range nodes {
		if n.Parent != nil {
			if p := a.local(b, *n.Parent); p != nil && shown[p.Number] {
				continue
			}
		}
		a.printf("%s\n", a.line(n))
		walk(n, "")
	}
}

var mermaidEscape = strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;")

// mermaid prints a flowchart: solid edges from parent to sub-issue, dotted
// edges from blocker to blocked. Closed issues are dashed.
func (a *App) mermaid(b *board, nodes []*Node) {
	shown := map[int]bool{}
	for _, n := range nodes {
		shown[n.Number] = true
	}
	a.printf("flowchart TD\n")
	var closed []string
	for _, n := range nodes {
		a.printf("  i%d[\"#%d %s<br/>%s\"]\n", n.Number, n.Number, mermaidEscape.Replace(n.Title), mermaidEscape.Replace(a.fieldText(n.Fields)))
		if n.State == "closed" {
			closed = append(closed, fmt.Sprintf("i%d", n.Number))
		}
	}
	for _, n := range nodes {
		for _, r := range n.SubIssues {
			if m := a.local(b, r); m != nil && shown[m.Number] {
				a.printf("  i%d --> i%d\n", n.Number, m.Number)
			}
		}
	}
	for _, n := range nodes {
		for _, r := range n.BlockedBy {
			if m := a.local(b, r); m != nil && shown[m.Number] {
				a.printf("  i%d -. blocks .-> i%d\n", m.Number, n.Number)
			}
		}
	}
	if len(closed) > 0 {
		a.printf("  classDef closed stroke-dasharray: 4 4\n  class %s closed\n", strings.Join(closed, ","))
	}
}

// Show prints one issue's fields, relations, PRs and lock.
func (a *App) Show(number int, asJSON bool) error {
	b, err := a.load(func(it *gh.Item) bool { return it.Issue.Number == number })
	if err != nil {
		return err
	}
	n := b.byNum[number]
	if n == nil {
		return fmt.Errorf("issue #%d is not on project %s #%d", number, a.Cfg.Project.Owner, a.Cfg.Project.Number)
	}
	if asJSON {
		return writeJSON(a.Out, n)
	}
	a.printf("#%d %s (%s)\n%s\n", n.Number, n.Title, n.State, n.URL)
	a.printf("Fields: %s\n", a.fieldText(n.Fields))
	if len(n.Labels) > 0 {
		a.printf("Labels: %s\n", strings.Join(n.Labels, ", "))
	}
	if n.Parent != nil {
		a.printf("Parent: %s\n", a.refLine(b, *n.Parent))
	}
	for _, l := range []struct {
		name string
		refs []gh.Ref
	}{{"Sub-issues", n.SubIssues}, {"Blocked by", n.BlockedBy}, {"Blocking", n.Blocking}} {
		if len(l.refs) == 0 {
			continue
		}
		a.printf("%s:\n", l.name)
		for _, r := range l.refs {
			a.printf("  %s\n", a.refLine(b, r))
		}
	}
	for _, pr := range n.OpenPRs {
		a.printf("Open PR: #%d\n", pr)
	}
	if n.Lock != nil {
		a.printf("Lock: %s at %s from %s, %s old\n", n.Lock.Branch, n.Lock.SHA, n.Lock.Host, time.Since(n.Lock.Since).Round(time.Second))
	}
	return nil
}

// refLine names a relation target with its title and, for an open board issue, its Status.
func (a *App) refLine(b *board, r gh.Ref) string {
	s := a.refName(r) + " " + r.Title
	if r.State == "closed" {
		return s + " (closed)"
	}
	if status := b.status[r.Number]; status != "" && strings.EqualFold(r.Repo, a.Cfg.Repo) {
		return s + " [" + status + "]"
	}
	return s
}
