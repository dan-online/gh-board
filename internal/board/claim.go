package board

import (
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
}

// claim is one attempt's state, kept so a failure can undo exactly what it made.
type claim struct {
	a        *App
	number   int
	branch   string
	worktree string
	sha      string
	pushed   bool
	pr       *gh.PR
	moved    bool
}

// Claim creates the branch, worktree, empty commit and draft PR for a ready issue.
func (a *App) Claim(o ClaimOpts) (err error) {
	issue, item, err := a.item(o.Number)
	if err != nil {
		return err
	}
	if err := a.eligible(issue, item); err != nil {
		return err
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
	c := &claim{a: a, number: o.Number, branch: fmt.Sprintf("%d-%s", o.Number, slug)}
	c.worktree = filepath.Join(main, a.Cfg.Worktrees, c.branch)
	if _, err := os.Lstat(c.worktree); err == nil {
		return fmt.Errorf("worktree path already exists: %s", c.worktree)
	}
	remote, base := a.Cfg.Remote, repo.DefaultBranch
	if _, err := a.Git.Run("fetch", remote, base); err != nil {
		return err
	}
	if out, err := a.Git.Run("ls-remote", "--heads", remote, "refs/heads/"+c.branch); err != nil {
		return err
	} else if out != "" {
		return fmt.Errorf("branch %s already exists on %s", c.branch, remote)
	}
	if _, err := a.Git.Run("worktree", "add", "-b", c.branch, c.worktree, remote+"/"+base); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			c.undo()
		}
	}()

	wt := git.Git{Dir: c.worktree}
	if _, err := wt.Run("commit", "--allow-empty", "-m", fmt.Sprintf("chore: claim #%d", o.Number)); err != nil {
		return err
	}
	if c.sha, err = wt.Run("rev-parse", "HEAD"); err != nil {
		return err
	}
	if _, err := wt.Run("push", "--force-with-lease=refs/heads/"+c.branch+":", "-u", remote, c.branch); err != nil {
		return err
	}
	c.pushed = true

	keyword := "Closes"
	if o.Refs {
		keyword = "Refs"
	}
	if c.pr, err = a.GH.CreatePR(o.Title, c.branch, base, fmt.Sprintf("%s #%d", keyword, o.Number)); err != nil {
		c.pr = nil
		return fmt.Errorf("open draft PR: %w", err)
	}
	if err := a.GH.SetFields(item.ID, map[string]string{"Status": a.Cfg.Statuses.Claimed}); err != nil {
		return fmt.Errorf("set Status %s: %w", a.Cfg.Statuses.Claimed, err)
	}
	c.moved = true

	if _, after, err := a.item(o.Number); err != nil {
		return err
	} else if older := olderThan(after.OpenPRs, c.pr.Number); len(older) > 0 {
		c.moved = false
		return exitf(ExitHeld, "older open linked PR found: %v. Backing out this claim", older)
	}
	a.printf("%s\n%s\n", c.worktree, c.pr.HTMLURL)
	return nil
}

// eligible is the plain-claim rule shared by claim and next.
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

func olderThan(prs []int, n int) []int {
	var older []int
	for _, p := range prs {
		if p < n {
			older = append(older, p)
		}
	}
	return older
}

// undo removes what this attempt made: its draft, its branch while the
// remote still holds the claim commit, and its worktree.
func (c *claim) undo() {
	a := c.a
	var left []string
	if c.pr != nil {
		if err := a.GH.ClosePR(c.pr.Number); err != nil {
			left = append(left, fmt.Sprintf("close PR #%d", c.pr.Number))
		}
	}
	if c.pushed {
		lease := fmt.Sprintf("--force-with-lease=refs/heads/%s:%s", c.branch, c.sha)
		if _, err := a.Git.Run("push", lease, a.Cfg.Remote, ":refs/heads/"+c.branch); err != nil {
			left = append(left, "delete remote branch "+c.branch)
		}
	}
	if _, err := a.Git.Run("worktree", "remove", "--force", c.worktree); err != nil {
		left = append(left, "remove worktree "+c.worktree)
	} else if _, err := a.Git.Run("branch", "-D", c.branch); err != nil {
		left = append(left, "delete local branch "+c.branch)
	}
	if c.moved {
		a.warnf("Issue #%d may still be %s. If no other claim holds it, run: gh board status %d %q\n",
			c.number, a.Cfg.Statuses.Claimed, c.number, a.Cfg.Statuses.Ready)
	}
	if len(left) > 0 {
		a.warnf("Cleanup could not: %s\n", strings.Join(left, "; "))
	}
}
