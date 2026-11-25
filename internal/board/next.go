package board

import (
	"encoding/json"
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
	list, err := a.unlocked(a.queue(items))
	if err != nil {
		return err
	}
	if list, err = a.stackHints(list); err != nil {
		return err
	}
	if limit > 0 && len(list) > limit {
		list = list[:limit]
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
// Issues with open blockers stay in, marked, for stackHints to settle.
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

// unlocked drops candidates whose lock is still live: no PR yet, or an open one.
func (a *App) unlocked(list []Candidate) ([]Candidate, error) {
	locks, err := a.remoteRefs("refs/claims/*")
	if err != nil || len(locks) == 0 {
		return list, err
	}
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
		out = append(out, c)
	}
	return out, nil
}

// stackHints keeps a blocked candidate only when it can stack on its one open
// blocker's claim, and records which issue that is.
func (a *App) stackHints(list []Candidate) ([]Candidate, error) {
	var out []Candidate
	for _, c := range list {
		if !c.blocked {
			out = append(out, c)
			continue
		}
		blockers, err := a.GH.BlockedBy(c.Number)
		if err != nil {
			return nil, err
		}
		var open []int
		for _, b := range blockers {
			if b.State == "open" {
				open = append(open, b.Number)
			}
		}
		if len(open) != 1 {
			continue
		}
		if _, err := a.stackParentOf(c.Number, open[0], blockers); err == nil {
			c.StackOn = open[0]
			out = append(out, c)
		}
	}
	return out, nil
}
