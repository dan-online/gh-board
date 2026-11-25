package board

import (
	"fmt"
	"time"

	"github.com/dan-online/gh-board/internal/gh"
)

// Merge lands an issue's claim PR through GitHub's async merge, which stacked
// PRs require, and deletes its branch once nothing builds on it.
func (a *App) Merge(n int, reviewed, method string) error {
	l, err := a.readLock(n)
	if err != nil {
		return err
	}
	if l == nil {
		return fmt.Errorf("issue #%d has no claim lock; merge its PR with gh pr merge", n)
	}
	found, err := a.lockPR(l)
	if err != nil {
		return err
	}
	if found == nil {
		return fmt.Errorf("issue #%d's claim has no PR", n)
	}
	return a.mergePR(a.GH, found.Number, reviewed, method)
}

// mergeAPI is the GitHub side of a merge.
type mergeAPI interface {
	PR(number int) (*gh.PR, error)
	StackFor(pr int) (*gh.Stack, error)
	MergeAsync(pr int, method, headSHA string) (*gh.AsyncMerge, error)
	MergeAsyncResult(pr int, uuid string) (*gh.AsyncMerge, error)
	OpenPRsOnBase(branch string) ([]gh.PR, error)
}

func (a *App) mergePR(api mergeAPI, number int, reviewed, method string) error {
	pr, err := api.PR(number)
	if err != nil {
		return err
	}
	switch {
	case pr.State != "open":
		return fmt.Errorf("PR #%d is %s", pr.Number, pr.State)
	case pr.Draft:
		return fmt.Errorf("PR #%d is a draft; mark it ready for review first", pr.Number)
	case reviewed != "" && reviewed != pr.Head.SHA:
		return fmt.Errorf("PR #%d's head is %s, not the reviewed %s; review the new head first", pr.Number, pr.Head.SHA, reviewed)
	}
	st, err := api.StackFor(pr.Number)
	if err != nil {
		return err
	}
	if below := openBelow(st, pr.Number); below != 0 {
		// Merging lands every PR below too, and those may not have passed review or CI.
		return fmt.Errorf("PR #%d sits above open PR #%d in stack %d; merge the stack bottom-up, starting with #%d", pr.Number, below, st.Number, below)
	}
	m, err := api.MergeAsync(pr.Number, method, pr.Head.SHA)
	if err != nil {
		if gh.Definite(err) {
			return fmt.Errorf("merge PR #%d: %w", pr.Number, err)
		}
		return exitf(ExitIncomplete, "merge PR #%d had no clear outcome: %v\nCheck the PR before merging again", pr.Number, err)
	}
	uuid := m.Details.UUID
	failures := 0
	for deadline := time.Now().Add(10 * time.Minute); m.Status == "pending" && time.Now().Before(deadline); {
		time.Sleep(3 * time.Second)
		next, err := api.MergeAsyncResult(pr.Number, uuid)
		if err != nil {
			if failures++; failures == 5 {
				return exitf(ExitIncomplete, "merge PR #%d was submitted, but its result cannot be read: %v\nCheck the PR before merging again", pr.Number, err)
			}
			continue
		}
		failures, m = 0, next
	}
	switch m.Status {
	case "merged":
	case "enqueued":
		return exitf(ExitIncomplete, "PR #%d is in the merge queue, not merged yet; check it later", pr.Number)
	case "failed":
		return fmt.Errorf("merge PR #%d failed: %s", pr.Number, m.Details.Message)
	default:
		return exitf(ExitIncomplete, "merge PR #%d is still %s after 10 minutes; check it before merging again", pr.Number, m.Status)
	}
	a.printf("merged PR #%d as %s\n", pr.Number, m.Details.SHA)

	branch := pr.Head.Ref
	if on, err := api.OpenPRsOnBase(branch); err != nil || len(on) > 0 {
		a.notef("kept branch %s: open PRs still build on it or the check failed\n", branch)
		return nil
	}
	if res, _, _ := a.pushRef("refs/heads/"+branch, "", pr.Head.SHA); res != pushed {
		a.notef("could not delete branch %s; it moved or the push failed\n", branch)
	}
	return nil
}

// openBelow returns the lowest open PR under pr in its stack, or 0.
func openBelow(st *gh.Stack, pr int) int {
	if st == nil {
		return 0
	}
	for _, p := range st.PullRequests {
		if p.Number == pr {
			return 0
		}
		if p.State == "open" {
			return p.Number
		}
	}
	return 0
}
