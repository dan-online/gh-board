package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dan-online/gh-board/internal/gh"
	"github.com/dan-online/gh-board/internal/git"
)

type ClaimOpts struct {
	Number int
	Title  string // PR title, and so the squash commit subject
	Slug   string
	Refs   bool // write Refs #N, so merging leaves the issue open
	On     int  // stack on this blocking issue's claim PR instead of the default branch
}

// claimAPI is the GitHub side of a claim after its lock.
type claimAPI interface {
	CreatePR(title, head, base, body string) (*gh.PR, error)
	ClosePR(number int) error
	PRsForBranch(branch string) ([]gh.PR, error)
	SetFields(itemID int64, values map[string]string) error
	StackFor(pr int) (*gh.Stack, error)
	CreateStack(prs ...int) (*gh.Stack, error)
	AddToStack(stack int, prs ...int) (*gh.Stack, error)
	PR(number int) (*gh.PR, error)
}

// parent is the claim a stacked claim builds on.
type parent struct {
	issue int
	pr    *gh.PR
	stack *gh.Stack // nil when the parent's PR is in no stack yet
	head  string    // the parent's head when this claim started
}

// claim is one attempt's state, kept so a failure undoes exactly what it made.
type claim struct {
	a        *App
	gh       claimAPI
	number   int
	branch   string
	worktree string
	lock     lock
	pushed   bool
	pr       *gh.PR
	local    bool // this attempt created the local branch
	stacked  bool // this attempt's PR is in a stack, so rollback must not close it
}

// Claim takes the issue's lock, then creates the branch, worktree and draft PR.
func (a *App) Claim(o ClaimOpts) error {
	issue, item, err := a.item(o.Number)
	if err != nil {
		return err
	}
	if err := a.eligible(issue, item); err != nil {
		return err
	}
	held, err := a.readLock(o.Number)
	if err != nil {
		return err
	}
	expect := ""
	if held != nil {
		done, pr, err := a.takeable(held)
		if err != nil {
			return err
		}
		if !done {
			return exitf(ExitHeld, "issue #%d is claimed: %s%s", o.Number, held, describePR(pr))
		}
		expect = held.SHA
	}
	slug := o.Slug
	if slug == "" {
		slug = Slugify(issue.Title)
	}
	if !slugPattern.MatchString(slug) {
		return exitf(ExitUsage, "invalid slug: %q", slug)
	}
	if err := a.checkRemote(); err != nil {
		return err
	}
	repo, err := a.GH.Repo()
	if err != nil {
		return err
	}
	main, err := a.mainCheckout()
	if err != nil {
		return err
	}
	c := &claim{a: a, gh: a.GH, number: o.Number, branch: fmt.Sprintf("%d-%s", o.Number, slug)}
	c.worktree = filepath.Join(main, a.Cfg.Worktrees, c.branch)
	if _, err := os.Lstat(c.worktree); err == nil {
		return fmt.Errorf("worktree path already exists: %s", c.worktree)
	}
	remote, base := a.Cfg.Remote, repo.DefaultBranch
	var up *parent
	if o.On != 0 {
		if up, err = a.stackParent(o.Number, o.On); err != nil {
			return err
		}
		base = up.pr.Head.Ref
	}
	if _, err := a.Git.Run("fetch", remote, base); err != nil {
		return err
	}
	if sha, err := a.remoteRef("refs/heads/" + c.branch); err != nil {
		return err
	} else if sha != "" {
		return fmt.Errorf("branch %s already exists on %s", c.branch, remote)
	}
	baseSHA, err := a.Git.Run("rev-parse", remote+"/"+base)
	if err != nil {
		return err
	}
	if up != nil {
		up.head = baseSHA
	}

	// The lock decides who holds the issue. Everything after it belongs to this attempt.
	c.lock = lock{Ref: lockRef(o.Number), Attempt: newAttempt(), Branch: c.branch}
	if c.lock.SHA, err = a.claimCommit(o.Number, baseSHA, c.branch, c.lock.Attempt); err != nil {
		return err
	}
	res, now, err := a.pushRef(c.lock.Ref, c.lock.SHA, expect)
	switch {
	case res == unknown:
		return exitf(ExitIncomplete, "claim outcome unknown: %v\nRun gh board check before claiming #%d again", err, o.Number)
	case res == rejected && now == "":
		return fmt.Errorf("could not create %s; nothing was claimed", c.lock.Ref)
	case res == rejected:
		other, err := a.describeLock(c.lock.Ref, now)
		if err != nil {
			return exitf(ExitHeld, "issue #%d was claimed first, by lock %s", o.Number, now)
		}
		return exitf(ExitHeld, "issue #%d was claimed first: %s", o.Number, other)
	}
	return c.finish(o, item, base, up)
}

// finish runs the steps after the lock. A definite failure undoes them and
// releases the lock; an unknown outcome keeps everything for gh board check.
func (c *claim) finish(o ClaimOpts, item *gh.Item, base string, up *parent) error {
	a := c.a
	if _, err := a.Git.Run("worktree", "add", "-b", c.branch, c.worktree, c.lock.SHA); err != nil {
		return c.fail(err)
	}
	c.local = true
	branchRef := "refs/heads/" + c.branch
	res, _, err := a.push(git.Git{Dir: c.worktree}, branchRef, c.lock.SHA, "", c.branch, "-u")
	switch res {
	case unknown:
		return c.keep("push "+c.branch, err)
	case rejected:
		return c.fail(fmt.Errorf("%s was created on %s by someone else", c.branch, a.Cfg.Remote))
	}
	c.pushed = true

	keyword := "Closes"
	if o.Refs {
		keyword = "Refs"
	}
	body := fmt.Sprintf("%s #%d\n\n%s", keyword, o.Number, attemptMarker(c.lock.Attempt))
	if c.pr, err = c.gh.CreatePR(o.Title, c.branch, base, body); err != nil {
		c.pr = nil
		if gh.Definite(err) {
			return c.fail(fmt.Errorf("open draft PR: %w", err))
		}
		prs, lookErr := c.gh.PRsForBranch(c.branch)
		found := attemptPR(prs, c.lock.Attempt)
		if lookErr != nil || found == nil {
			return c.keep("open draft PR", err)
		}
		c.pr = found
	}
	if up != nil {
		if err := c.register(up); err != nil {
			return err
		}
	}
	if err := c.gh.SetFields(item.ID, map[string]string{"Status": a.Cfg.Statuses.Claimed}); err != nil {
		if gh.Definite(err) {
			return c.fail(fmt.Errorf("set Status %s: %w", a.Cfg.Statuses.Claimed, err))
		}
		a.printf("%s\n%s\n", c.worktree, c.pr.HTMLURL)
		return exitf(ExitIncomplete, "claimed #%d, but setting Status %s had no clear outcome: %v\nCheck it, and if needed run: gh board status %d %q",
			c.number, a.Cfg.Statuses.Claimed, err, c.number, a.Cfg.Statuses.Claimed)
	}
	a.printf("%s\n%s\n", c.worktree, c.pr.HTMLURL)
	return nil
}

// keep stops without undoing anything, because a write may have happened.
func (c *claim) keep(step string, err error) error {
	return exitf(ExitIncomplete, "claim incomplete: %s had no clear outcome: %v\n"+
		"%s (attempt %s) still holds #%d, and nothing was undone. Run gh board check, then finish the claim by hand "+
		"or, once nothing of this attempt remains, run: gh board release %d --sha %s",
		step, err, c.lock.Ref, c.lock.Attempt, c.number, c.number, c.lock.SHA)
}

// fail undoes what this attempt made and releases the lock last. The PR
// closes only after the branch and worktree are gone, because a closed PR is
// what lets the next claim take the lock over. If any undo fails, the PR stays
// open and the lock stays, so no one claims over the leftovers.
func (c *claim) fail(cause error) error {
	if c.stacked {
		// Closing a PR in the middle of a stack blocks every PR above it.
		return c.keep("the claim after its PR joined the stack", cause)
	}
	a := c.a
	var left []string
	if c.pushed {
		if res, _, _ := a.pushRef("refs/heads/"+c.branch, "", c.lock.SHA); res != pushed {
			left = append(left, "delete remote branch "+c.branch)
		}
	}
	if c.local {
		if _, err := a.Git.Run("worktree", "remove", "--force", c.worktree); err != nil {
			left = append(left, "remove worktree "+c.worktree)
		} else if _, err := a.Git.Run("branch", "-D", c.branch); err != nil {
			left = append(left, "delete local branch "+c.branch)
		}
	}
	if c.pr != nil && len(left) == 0 {
		// Someone may have stacked the PR since it was opened; closing it would block the PRs above.
		if st, err := c.gh.StackFor(c.pr.Number); err != nil || st != nil {
			return c.keep("roll back a PR that may be in a stack", errors.Join(cause, err))
		}
		if err := c.gh.ClosePR(c.pr.Number); err != nil {
			left = append(left, fmt.Sprintf("close PR #%d", c.pr.Number))
		}
	}
	if len(left) == 0 {
		if res, _, _ := a.pushRef(c.lock.Ref, "", c.lock.SHA); res != pushed {
			left = append(left, fmt.Sprintf("release the lock: gh board release %d --sha %s", c.number, c.lock.SHA))
		}
	} else {
		if c.pr != nil {
			left = append(left, fmt.Sprintf("close PR #%d", c.pr.Number))
		}
		left = append(left, fmt.Sprintf("then release the lock: gh board release %d --sha %s", c.number, c.lock.SHA))
	}
	if len(left) > 0 {
		return fmt.Errorf("%w\nCleanup could not: %s", cause, strings.Join(left, "; "))
	}
	return cause
}

// eligible is the plain-claim rule shared by claim and next. The lock is checked separately.
func (a *App) eligible(issue *gh.Issue, item *gh.Item) error {
	switch {
	case issue.State != "open":
		return fmt.Errorf("issue #%d is closed", issue.Number)
	case item.Values["Status"] != a.Cfg.Statuses.Ready:
		return fmt.Errorf("issue #%d is not %s", issue.Number, a.Cfg.Statuses.Ready)
	case len(item.OpenPRs) > 0:
		return exitf(ExitHeld, "issue #%d already has an open linked PR: %v", issue.Number, item.OpenPRs)
	}
	return nil
}

func describePR(pr *gh.PR) string {
	if pr == nil {
		return ", no PR yet"
	}
	return fmt.Sprintf(", PR #%d %s", pr.Number, pr.State)
}

// stackParent checks that issue m can carry a stacked claim of n: m is n's only
// open blocker and its claim PR is open and on top of its stack, or unstacked.
func (a *App) stackParent(n, m int) (*parent, error) {
	blockers, err := a.GH.BlockedBy(n)
	if err != nil {
		return nil, err
	}
	return a.stackParentOf(n, m, blockers)
}

// stackParentOf is stackParent for blockers already read.
func (a *App) stackParentOf(n, m int, blockers []gh.Issue) (*parent, error) {
	isBlocker := false
	for _, b := range blockers {
		switch {
		case b.Number == m && b.State == "open":
			isBlocker = true
		case b.Number != m && b.State == "open":
			return nil, fmt.Errorf("--on #%d: #%d is also blocked by open #%d", m, n, b.Number)
		}
	}
	if !isBlocker {
		return nil, fmt.Errorf("--on #%d: #%d is not an open blocker of #%d", m, m, n)
	}
	l, err := a.readLock(m)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return nil, fmt.Errorf("--on #%d: #%d has no claim to stack on", m, m)
	}
	pr, err := a.lockPR(l)
	if err != nil {
		return nil, err
	}
	if pr == nil || pr.State != "open" {
		return nil, fmt.Errorf("--on #%d: #%d's claim has no open PR%s", m, m, describePR(pr))
	}
	st, err := a.GH.StackFor(pr.Number)
	if err != nil {
		return nil, err
	}
	if st != nil && st.Top() != pr.Number {
		return nil, fmt.Errorf("--on #%d: PR #%d is not the top of stack %d, and stacks are linear; wait for #%d or split the work",
			m, pr.Number, st.Number, m)
	}
	return &parent{issue: m, pr: pr, stack: st}, nil
}

// register puts this attempt's PR on top of the parent's, in a new stack or the parent's.
func (c *claim) register(up *parent) error {
	var err error
	if up.stack == nil {
		_, err = c.gh.CreateStack(up.pr.Number, c.pr.Number)
	} else {
		_, err = c.gh.AddToStack(up.stack.Number, c.pr.Number)
	}
	st, readErr := c.gh.StackFor(c.pr.Number)
	switch {
	case readErr != nil:
		return c.keep("register the stack", errors.Join(err, readErr))
	case st == nil && (err == nil || gh.Definite(err)):
		return c.fail(fmt.Errorf("stack PR #%d on #%d: %w", c.pr.Number, up.pr.Number, errors.Join(err, errors.New("the PR is in no stack"))))
	case st == nil:
		return c.keep("register the stack", err)
	}
	c.stacked = true
	switch below := st.Below(c.pr.Number); {
	case below == up.pr.Number:
	case below == 0 && c.parentMerged(up):
		// The parent landed during setup and GitHub moved this PR to the bottom.
	default:
		return c.keep("register the stack", fmt.Errorf("PR #%d is not directly above #%d in stack %d", c.pr.Number, up.pr.Number, st.Number))
	}
	if now, err := c.a.remoteRef("refs/heads/" + up.pr.Head.Ref); err == nil && now != "" && now != up.head {
		c.a.notef("#%d's branch moved during the claim; restack with: gh stack rebase\n", up.issue)
	}
	return nil
}

func (c *claim) parentMerged(up *parent) bool {
	pr, err := c.gh.PR(up.pr.Number)
	return err == nil && pr.MergedAt != nil
}
