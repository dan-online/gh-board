package main

import (
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/dan-online/gh-board/internal/board"
)

// skill renders the agent guide from the same model --help uses,
// plus the current repo's board settings when a config is loaded.
func skill(app *kong.Application, a *board.App) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("---\nname: gh-board\n")
	w("description: Files, readies, claims and moves issues on this repo's GitHub project board with gh board. ")
	w("Use before filing an issue, picking or claiming work, or changing an issue's Status.\n---\n\n")
	w("# gh board\n\n")
	if a != nil {
		c := a.Cfg
		w("This checkout's board: issues in %s, project %s #%d. ", c.Repo, c.Project.Owner, c.Project.Number)
		w("Statuses move %s, then %s, then %s when claimed.\n\n", c.Statuses.New, c.Statuses.Ready, c.Statuses.Claimed)
	}
	w("## Workflow\n\n")
	w("0. A checkout without `.github/board.yml` needs `gh board init` first. Without a terminal, pass its flags; it names any it needs.\n")
	w("1. File work with `gh board file`. Write the body under the form's `### ` headings. The issue starts in the new status.\n")
	w("2. When every required section is filled, run `gh board ready N`. It names each missing section or field.\n")
	w("3. Pick work with `gh board next`, then `gh board claim N 'type(scope): subject'`. The title becomes the squash commit subject.\n")
	w("4. Claim prints two lines: the worktree path, then the draft PR URL. Work and commit in that worktree and push its branch.\n")
	w("5. Move the issue on with `gh board status N STATUS` as the PR goes to review, and merge with `gh board merge N --sha REVIEWED_HEAD`.\n\n")
	w("## Stacking\n\n")
	w("When `next` shows `stack on #M`, claim with `gh board claim N TITLE --on M` to start before #M merges. Your branch starts at #M's PR head and your PR joins #M's GitHub stack. ")
	w("When #M's branch changes, run `gh stack rebase` in your worktree and push. Merge bottom-up with `gh board merge`; `gh pr merge` refuses stacked PRs. GitHub retargets your PR to the default branch once #M lands, and its closing keyword works as usual.\n\n")
	w("## Reading the board\n\n")
	w("Use `gh board graph` for the sub-issue tree with Status, fields and open blockers, and `gh board show N` for one issue's fields, relations, PRs and lock; both take `--json`. `gh board graph --mermaid` prints a chart for an issue or PR body. ")
	w("Add a parent or blockers after filing with `gh board link N --parent P --blocked-by M`. Reach for `gh api` only for what these do not print.\n\n")
	w("Every command reads GitHub first and keeps no local state. Run `gh board doctor` on a new machine before claiming.\n\n")

	w("## Commands\n\n")
	for _, n := range app.Leaves(true) {
		if n.Name == "skill" {
			continue
		}
		w("### gh board %s\n\n", n.Summary())
		w("%s", n.Help)
		if d := n.Detail; d != "" {
			w(" %s", d)
		}
		w("\n\n")
		for _, p := range n.Positional {
			w("- `<%s>`: %s\n", p.Name, p.Help)
		}
		for _, f := range n.Flags {
			if f.Hidden || f.Name == "help" {
				continue
			}
			w("- `%s`: %s\n", f.String(), f.Help)
		}
		w("\n")
	}

	w("## Exit codes\n\n")
	w("- 0: done.\n- 1: failed; the message says why and nothing more is needed to retry.\n")
	w("- %d: bad arguments.\n", board.ExitUsage)
	w("- %d: another claim holds the issue; the message names its branch, machine and PR. Pick other work.\n", board.ExitHeld)
	w("- %d: a write's outcome is unknown, for example after a timeout. Do not repeat the command blindly; look first.\n\n", board.ExitIncomplete)

	w("## When something fails\n\n")
	w("- `file` printed a URL and then failed: the issue exists. The message lists the writes still missing; make them by hand or with `gh board status`. Never file it again.\n")
	w("- `file` exited %d without a URL: the issue may exist. Search the repo's issues for the title before filing again.\n", board.ExitIncomplete)
	w("- `claim` failed: it removed what it made and released its lock, or says which steps it could not undo.\n")
	w("- `claim` exited %d: some write had no clear outcome, so it kept the lock and everything else. Run `gh board check`, finish the claim by hand, or release the lock with the command it printed once nothing of the attempt remains.\n", board.ExitIncomplete)
	w("- A lock with no PR in `gh board check` is a claim still setting up, or one whose session died. Release it only after confirming that session has stopped.\n")
	w("- A rate limit message: wait for the time it gives. gh board already waits out short secondary limits.\n")
	w("- `min_version`: this machine runs an older gh-board than the repo needs; reinstall at the version the message names.\n\n")

	if a != nil {
		c := a.Cfg
		w("## This repo's board\n\n")
		w("- Issue types: %s.\n", strings.Join(c.Types, ", "))
		for _, name := range c.FieldNames() {
			f := c.Fields[name]
			note := ""
			switch {
			case f.Required:
				note = ", required"
			case f.Default != "":
				note = ", default " + f.Default
			}
			w("- Field %s: %s%s. Pass it as `--field %s=VALUE`.\n", name, strings.Join(f.Options, ", "), note, name)
		}
		for _, f := range c.Forms {
			match := "any other issue"
			if f.Label != "" {
				match = "issues labelled " + f.Label
			}
			w("- Form %s checks %s", f.File, match)
			for _, name := range c.FieldNames() {
				if v, ok := f.Require[name]; ok {
					w(", and needs %s to be %s", name, strings.Join(v, " or "))
				}
			}
			w(".\n")
		}
		if c.Queue != nil {
			w("- `next` ranks by %s in the order %s.\n", c.Queue.Sort, strings.Join(c.Fields[c.Queue.Sort].Options, ", "))
		}
	}
	return b.String()
}
