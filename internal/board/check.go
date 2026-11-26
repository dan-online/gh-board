package board

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/dan-online/gh-board/internal/gh"
)

// Check reports where locks, PRs and Status disagree. It changes nothing.
func (a *App) Check() error {
	locks, err := a.remoteRefs("refs/claims/*")
	if err != nil {
		return err
	}
	items, err := a.GH.Items()
	if err != nil {
		return err
	}
	byNumber := map[int]*gh.Item{}
	for i := range items {
		byNumber[items[i].Issue.Number] = &items[i]
	}
	numbers := map[string]int{}
	locked := map[int]bool{}
	var shas []string
	for ref, sha := range locks {
		if n, err := strconv.Atoi(strings.TrimPrefix(ref, "refs/claims/")); err == nil {
			numbers[ref] = n
			locked[n] = true
			shas = append(shas, sha)
		}
	}
	commits, err := a.GH.Commits(shas)
	if err != nil {
		return err
	}
	problems := 0
	for _, ref := range slices.Sorted(maps.Keys(numbers)) {
		n, sha := numbers[ref], locks[ref]
		result := commits[sha]
		var l *lock
		err := result.Err
		if err == nil {
			l, err = parseLock(ref, sha, result.Commit)
		} else {
			err = fmt.Errorf("read lock at %s: %w", sha, err)
		}
		if err != nil {
			a.printf("#%d\t%s: %v\n", n, ref, err)
			problems++
			continue
		}
		pr, err := a.lockPR(l)
		if err != nil {
			return err
		}
		state, status := "", ""
		it := byNumber[n]
		var stateErr error
		if it != nil {
			state, status = it.Issue.State, it.Values["Status"]
		} else if issue, err := a.GH.Issue(n); err != nil {
			stateErr = err
		} else {
			state = issue.State
		}
		var note string
		switch {
		case state == "closed":
			note = "issue closed; the lock blocks nothing"
		case pr == nil:
			note = "setup or orphan: no PR for this attempt. Release it once the claiming session has stopped: gh board release " + strconv.Itoa(n) + " --sha " + l.SHA
			problems++
		case pr.State == "closed":
			note = fmt.Sprintf("PR #%d %s; the next claim takes the lock over", pr.Number, map[bool]string{true: "merged", false: "closed"}[pr.MergedAt != nil])
		default:
			note = fmt.Sprintf("PR #%d open", pr.Number)
			if it == nil && state == "open" {
				note += "; not on the board"
				problems++
			} else if it != nil && status == a.Cfg.Statuses.Ready {
				note += fmt.Sprintf("; Status is still %s", status)
				problems++
			}
		}
		if stateErr != nil {
			note += fmt.Sprintf("; issue state could not be verified: %v", stateErr)
			problems++
		}
		a.printf("#%d\t%s\t%s\n", n, l, note)
	}
	for _, it := range items {
		if it.Issue.State != "open" || locked[it.Issue.Number] {
			continue
		}
		switch status := it.Values["Status"]; {
		case status == a.Cfg.Statuses.Claimed:
			a.printf("#%d\t%s with no lock\n", it.Issue.Number, status)
			problems++
		case status == a.Cfg.Statuses.Ready && len(it.OpenPRs) > 0:
			a.printf("#%d\t%s with open PR %v and no lock\n", it.Issue.Number, status, it.OpenPRs)
			problems++
		}
	}
	if problems == 0 {
		a.printf("no drift\n")
	}
	return nil
}
