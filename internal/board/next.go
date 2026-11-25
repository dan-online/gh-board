package board

import (
	"encoding/json"
	"slices"

	"github.com/dan-online/gh-board/internal/gh"
)

type Candidate struct {
	Number int               `json:"number"`
	Title  string            `json:"title"`
	URL    string            `json:"url"`
	Fields map[string]string `json:"fields"`
}

// Next lists claimable issues that nothing blocks, best first.
func (a *App) Next(limit int, asJSON bool) error {
	items, err := a.GH.Items()
	if err != nil {
		return err
	}
	list := a.queue(items)
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
		a.printf("#%d\t%s\n", c.Number, c.Title)
	}
	return nil
}

// queue filters and ranks items. Ranking is by the queue field's option
// order, unset last, then board position, which the stable sort keeps.
func (a *App) queue(items []gh.Item) []Candidate {
	var list []Candidate
	for i := range items {
		it := &items[i]
		if it.Archived || it.Issue.Dependencies.BlockedBy > 0 || a.eligible(&it.Issue, it) != nil {
			continue
		}
		list = append(list, Candidate{Number: it.Issue.Number, Title: it.Issue.Title, URL: it.Issue.HTMLURL, Fields: it.Values})
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
