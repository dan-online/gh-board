package board

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/dan-online/gh-board/internal/gh"
)

type Candidate struct {
	Number  int               `json:"number"`
	Title   string            `json:"title"`
	URL     string            `json:"url"`
	Fields  map[string]string `json:"fields"`
	StackOn int               `json:"stack_on,omitempty"` // claim with --on this issue

	blocked bool
}

// Next lists claimable issues that nothing blocks, best first.
func (a *App) Next(limit int, asJSON bool) error {
	items, err := a.GH.Items()
	if err != nil {
		return err
	}
	list, err := a.claimable(a.queue(items), limit)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		if list == nil {
			list = []Candidate{}
		}
		return enc.Encode(list)
	}
	for _, c := range list {
		if c.StackOn != 0 {
			a.printf("#%d\t%s\tstack on #%d\n", c.Number, c.Title, c.StackOn)
		} else {
			a.printf("#%d\t%s\n", c.Number, c.Title)
		}
	}
	return nil
}

// queue filters and ranks items. Ranking is by the queue field's option
// order, unset last, then board position, which the stable sort keeps.
// Issues with open blockers stay in, marked, for stackHint to settle.
func (a *App) queue(items []gh.Item) []Candidate {
	var list []Candidate
	for i := range items {
		it := &items[i]
		if it.Archived || a.eligible(&it.Issue, it) != nil {
			continue
		}
		list = append(list, Candidate{Number: it.Issue.Number, Title: it.Issue.Title, URL: it.Issue.HTMLURL,
			Fields: it.Values, blocked: it.Issue.Dependencies.BlockedBy > 0})
	}
	if a.Cfg.Queue != nil {
		order := a.Cfg.Fields[a.Cfg.Queue.Sort].Options
		rank := func(c Candidate) int {
			if r := slices.Index(order, c.Fields[a.Cfg.Queue.Sort]); r >= 0 {
				return r
			}
			return len(order)
		}
		slices.SortStableFunc(list, func(x, y Candidate) int { return rank(x) - rank(y) })
	}
	return list
}

func (a *App) claimable(list []Candidate, limit int) ([]Candidate, error) {
	locks, err := a.remoteRefs("refs/claims/*")
	if err != nil {
		return nil, err
	}
	parents := map[int]bool{}
	var out []Candidate
	for _, c := range list {
		ref := lockRef(c.Number)
		if sha, ok := locks[ref]; ok {
			l, err := a.describeLock(ref, sha)
			if err != nil {
				return nil, err
			}
			if done, _, err := a.takeable(l); err != nil {
				return nil, err
			} else if !done {
				continue
			}
		}
		if c.blocked {
			c.StackOn, err = a.stackHint(c.Number, parents)
			if err != nil {
				return nil, err
			}
			if c.StackOn == 0 {
				continue
			}
		}
		out = append(out, c)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// stackHint returns the one open blocker's issue number if its claim can carry n.
func (a *App) stackHint(n int, parents map[int]bool) (int, error) {
	blockers, err := a.GH.BlockedBy(n)
	if err != nil {
		return 0, err
	}
	m := 0
	for _, b := range blockers {
		if b.State != "open" {
			continue
		}
		if m != 0 || !a.isRepoIssue(b) {
			return 0, nil
		}
		m = b.Number
	}
	if m == 0 {
		return 0, nil
	}
	ok, cached := parents[m]
	if !cached {
		_, err := a.readStackParent(m)
		var no notStackable
		if err != nil && !errors.As(err, &no) {
			return 0, err
		}
		ok = err == nil
		parents[m] = ok
	}
	if !ok {
		return 0, nil
	}
	return m, nil
}
