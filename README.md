# gh board

A GitHub CLI extension for running an issue board on a GitHub project, built for repos where coding agents file and claim the work

It files issues with their project fields, checks an issue against its issue form before it moves to ready, and claims work by opening a branch, a worktree and a draft PR in one go. Everything lives on GitHub, so there's no local state to sync between machines.

## Installation

```sh
gh extension install dan-online/gh-board --pin v0.1.0
gh board doctor
```

To move to a newer release, reinstall at its tag:

```sh
gh extension remove board
gh extension install dan-online/gh-board --pin v0.2.0
```

Board writes need the `project` scope (`gh auth refresh -s project`), write access to the project and push access to the repo. `gh board doctor` checks all of that.

## Usage

Run `gh board init` in the repo. It uses the project linked to the repo, or offers to create one with Status, Size and Priority fields, then writes `.github/board.yml`. Without a terminal it takes flags instead of asking (`gh board init --help`), and it only creates or changes a project when you pass `--create`.

The config it writes looks like this:

```yaml
repo: owner/repo
remote: origin
project: { owner: owner, number: 1 }
api_version: "2026-03-10"
worktrees: .worktrees
types: [Task, Bug, Feature]
statuses: { new: Inbox, ready: Ready, claimed: In progress }
fields:
  Size: { options: [S, M, L], required: true }
  Priority: { options: [Now, Next], default: Next }
queue: { sort: Priority }
forms:
  - { label: decision, file: decision.yml }
  - { default: true, file: work.yml, require: { Size: [S, M] } }
```

`project.owner` can be an organization or a user. `forms` point at files in `.github/ISSUE_TEMPLATE`, and the first form whose label the issue has wins. `min_version: vX.Y.Z` makes older builds refuse to run in the repo.

Then:

```sh
gh board file --type Task --title "Add the thing" --body-file body.md --field Size=S
gh board ready 42
gh board next
gh board claim 42 "feat(api): add the thing"
gh board status 42 "In review"
```

`claim` prints the worktree path and the draft PR URL. Pass `--refs` to write `Refs #42` instead of `Closes #42` for work that only closes after a deploy.

Each claim takes a lock, the ref `refs/claims/42`, pushed with a lease, so two machines claiming the same issue can't both win. The ref doesn't show up in branch lists or start any workflows. `gh board check` lists locks next to their PRs and Statuses, and `gh board release 42 --sha SHA` clears a lock whose claim died before opening its PR.

If 43 is blocked only by 42 and 42 already has a claim PR, `gh board next` shows `stack on #42`, and `gh board claim 43 "feat: ..." --on 42` builds on 42's branch and puts both PRs in a GitHub stack. Stacked PRs can't be merged with `gh pr merge`, so merge with `gh board merge 42 --sha REVIEWED_HEAD`. It works for any claimed PR, waits for the merge and deletes the branch once nothing targets it.

`gh board skill` prints a guide for agents, with the repo's own fields and forms filled in. Point your agent instructions at it rather than copying the commands around.

## Development

```sh
nix develop
go test ./...
```

## License

MIT
