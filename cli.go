package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
	"github.com/cli/go-gh/v2/pkg/prompter"
	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/dan-online/gh-board/internal/board"
)

type env struct {
	version string
	model   *kong.Application
}

func (e *env) app() (*board.App, error) { return e.open(board.Open) }

func (e *env) open(open func(string, string) (*board.App, error)) (*board.App, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return open(dir, e.version)
}

type CLI struct {
	Version kong.VersionFlag `help:"Print the version and exit."`

	Init    InitCmd    `cmd:"" help:"Write .github/board.yml for this repo, creating a project if it has none."`
	File    FileCmd    `cmd:"" help:"Create an issue on the project with its fields and relations."`
	Ready   ReadyCmd   `cmd:"" help:"Check an issue against its form and move it to the ready status."`
	Claim   ClaimCmd   `cmd:"" help:"Claim a ready issue: branch, worktree, empty commit and draft PR."`
	Status  StatusCmd  `cmd:"" help:"Set an issue's project Status."`
	Check   CheckCmd   `cmd:"" help:"Report claim locks, PRs and Statuses that disagree. Changes nothing."`
	Release ReleaseCmd `cmd:"" help:"Delete an issue's claim lock, only while it holds the given commit."`
	Merge   MergeCmd   `cmd:"" help:"Merge an issue's claim PR, stacked or not, and delete its branch."`
	Next    NextCmd    `cmd:"" help:"List claimable issues that nothing blocks, best first."`
	Graph   GraphCmd   `cmd:"" help:"Draw the board's issues as a sub-issue tree with their blockers."`
	Show    ShowCmd    `cmd:"" help:"Print one issue's fields, relations, PRs and claim lock."`
	Link    LinkCmd    `cmd:"" help:"Add a parent or blockers to an existing issue."`
	Doctor  DoctorCmd  `cmd:"" help:"Check this machine's token, access, remote and config."`
	Skill   SkillCmd   `cmd:"" help:"Print the agent guide for this repo's board."`
}

type InitCmd struct {
	Remote      string   `default:"origin" help:"Remote whose URL names the repo."`
	Project     string   `placeholder:"OWNER/NUMBER" help:"Project to use; defaults to the one linked to the repo."`
	Create      bool     `help:"Allow init to create a project, or add missing Status options. Without a terminal this is the only way to consent."`
	Force       bool     `help:"Replace an existing .github/board.yml."`
	New         string   `placeholder:"STATUS" help:"Status option for new issues (default Inbox)."`
	Ready       string   `placeholder:"STATUS" help:"Status option for issues ready to claim (default Ready)."`
	Claimed     string   `placeholder:"STATUS" help:"Status option for claimed issues (default In progress)."`
	Field       []string `sep:"none" placeholder:"NAME" help:"Single-select project field to set and check; repeat for more. Defaults to all of them."`
	Required    []string `sep:"none" placeholder:"NAME" help:"Field that must be given when filing; repeat for more."`
	Default     []string `sep:"none" placeholder:"NAME=VALUE" help:"Default value for a field; repeat for more."`
	Sort        string   `placeholder:"NAME" help:"Field whose option order ranks gh board next (default Priority when present)."`
	Form        []string `sep:"none" placeholder:"LABEL=FILE" help:"Issue form in .github/ISSUE_TEMPLATE for issues with LABEL; repeat for more."`
	DefaultForm string   `placeholder:"FILE" help:"Issue form for issues no --form label matches."`
	Type        []string `sep:"none" help:"Issue type issues may use; repeat for more. Defaults to the repo's enabled types."`
}

func (c *InitCmd) Help() string {
	return "Uses the project linked to the repo and reads its Status and single-select fields, the repo's issue types " +
		"and its issue forms. With no linked project it offers to create one with Status Inbox, Ready, In progress, " +
		"In review and Done, plus Size and Priority fields. On a terminal it asks; otherwise every choice it cannot make " +
		"is an error naming the flag to pass, and nothing is written to GitHub without --create."
}

func (c *InitCmd) Run(e *env) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	var ask board.Asker
	if term.IsTerminal(os.Stdin) && term.FromEnv().IsTerminalOutput() && os.Getenv("GH_PROMPT_DISABLED") == "" {
		ask = prompter.New(os.Stdin, os.Stdout, os.Stderr)
	}
	return board.Init(dir, board.InitOpts{Remote: c.Remote, Project: c.Project, Create: c.Create, Force: c.Force,
		New: c.New, Ready: c.Ready, Claimed: c.Claimed, Fields: c.Field, Required: c.Required, Defaults: c.Default,
		Sort: c.Sort, Forms: c.Form, DefaultForm: c.DefaultForm, Types: c.Type}, ask, os.Stdout)
}

type FileCmd struct {
	Type      string   `required:"" help:"Issue type, one of the config's types."`
	Title     string   `required:"" help:"Issue title."`
	BodyFile  string   `required:"" type:"existingfile" help:"File holding the issue body, written to the form's ### headings."`
	Field     []string `sep:"none" placeholder:"NAME=VALUE" help:"Project field value; repeat for more. Required fields must be given; others take their default."`
	Label     []string `sep:"none" help:"Label to add; repeat for more."`
	Parent    string   `placeholder:"ISSUE" help:"Parent issue, as a number or URL."`
	BlockedBy []string `placeholder:"ISSUES" help:"Blocking issues, as comma-separated numbers or URLs."`
}

func (c *FileCmd) Help() string {
	return "Checks the type, field values, labels and relations before creating anything. " +
		"Prints the issue URL, then adds the issue to the project with the new status. " +
		"A failure after the URL lists the writes still missing."
}

func (c *FileCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.File(board.FileOpts{Type: c.Type, Title: c.Title, BodyFile: c.BodyFile, Fields: c.Field,
		Labels: c.Label, Parent: c.Parent, BlockedBy: c.BlockedBy})
}

type ReadyCmd struct {
	Issue int `arg:"" help:"Issue number."`
}

func (c *ReadyCmd) Help() string {
	return "Picks the form by label, else the default form. A required ### section counts as missing when it is " +
		"empty, _No response_, or only list markers and checkboxes. Then checks the form's required field values."
}

func (c *ReadyCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Ready(c.Issue)
}

type ClaimCmd struct {
	Issue int    `arg:"" help:"Issue number."`
	Title string `arg:"" help:"PR title, and so the squash commit subject: a conventional commit such as 'feat(api): add jobs'."`
	Slug  string `arg:"" optional:"" help:"Branch slug; defaults to the slugified issue title."`
	Refs  bool   `help:"Write Refs #N instead of Closes #N, for issues that close after a deploy."`
	On    int    `placeholder:"ISSUE" help:"Stack on this issue's claim PR: it must be the only open blocker, and its PR open and on top of its stack."`
}

func (c *ClaimCmd) Help() string {
	return "Requires the ready status and no open linked PR. Takes the lock refs/claims/ISSUE with a commit " +
		"'chore: claim #ISSUE' on the freshly fetched default branch; only one claim can win it, on any machine. A lock " +
		"whose PR is merged or closed is taken over. Then creates branch ISSUE-SLUG at that commit and a worktree under " +
		"the main checkout, pushes the branch, opens a draft PR and sets the claimed status. Prints the worktree path and " +
		"the PR URL. A failure removes only what this claim made and releases its lock; a write with no clear outcome " +
		"keeps everything and exits 4. With --on, the branch starts from the blocking issue's PR head, the draft PR " +
		"targets that branch, and both PRs join one GitHub stack, which GitHub retargets and rebases as lower PRs merge."
}

func (c *ClaimCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Claim(board.ClaimOpts{Number: c.Issue, Title: c.Title, Slug: c.Slug, Refs: c.Refs, On: c.On})
}

type StatusCmd struct {
	Issue  int    `arg:"" help:"Issue number."`
	Status string `arg:"" help:"Any option of the project's Status field. Quote names with spaces."`
}

func (c *StatusCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Status(c.Issue, c.Status)
}

type CheckCmd struct{}

func (c *CheckCmd) Help() string {
	return "Lists every lock with its attempt, host, age, branch and PR, flags locks with no PR, claim PRs whose " +
		"Status never moved, and claimed issues with no lock."
}

func (c *CheckCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Check()
}

type ReleaseCmd struct {
	Issue int    `arg:"" help:"Issue number."`
	SHA   string `required:"" name:"sha" help:"Commit the lock must still hold, from gh board check."`
}

func (c *ReleaseCmd) Help() string {
	return "Use it for a lock whose claim never opened a PR, after confirming the claiming session has stopped. " +
		"The delete is conditional, so a lock that changed since the check stays."
}

func (c *ReleaseCmd) Run(e *env) error {
	a, err := e.open(board.OpenLocal)
	if err != nil {
		return err
	}
	return a.Release(c.Issue, c.SHA)
}

type MergeCmd struct {
	Issue  int    `arg:"" help:"Issue number."`
	SHA    string `name:"sha" help:"Head commit the review approved; the merge refuses any other head."`
	Method string `default:"squash" enum:"squash,merge,rebase" help:"Merge method (squash, merge or rebase)."`
}

func (c *MergeCmd) Help() string {
	return "Uses GitHub's async merge, which stacked PRs require; gh pr merge refuses them. It refuses a PR that " +
		"sits above an open PR in its stack, so stacks merge bottom-up and every PR passes its own review. Waits for the result and exits 4 if the PR only joined a merge queue. Deletes the " +
		"branch afterwards unless another open PR still targets it."
}

func (c *MergeCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Merge(c.Issue, c.SHA, c.Method)
}

type NextCmd struct {
	Limit int  `default:"10" help:"Show at most this many issues; 0 shows all."`
	JSON  bool `name:"json" help:"Print JSON."`
}

func (c *NextCmd) Help() string {
	return "Lists open issues in the ready status with no open blockers, no open linked PR and no live claim lock, plus " +
		"issues whose only open blocker has a claim PR to stack on, marked 'stack on #M'. Ranks them by the " +
		"config's queue field, unset last, then board position. The list is advice: claim checks again."
}

func (c *NextCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Next(c.Limit, c.JSON)
}

type GraphCmd struct {
	Issue   int  `arg:"" optional:"" help:"Show only this issue and the issues connected to it."`
	Closed  bool `help:"Include closed issues."`
	JSON    bool `name:"json" xor:"format" help:"Print JSON: every issue with its fields, labels, relations, open PRs and lock."`
	Mermaid bool `xor:"format" help:"Print a Mermaid flowchart, which GitHub renders in issues and PRs."`
}

func (c *GraphCmd) Help() string {
	return "Covers open, unarchived issues on the board. Each issue whose parent is not shown starts a tree; its " +
		"sub-issues follow in sub-issue order. A line shows Status and the config's fields, then open blockers, open " +
		"PRs and the claim lock's branch. Blockers in other repos are named owner/repo#N. Mermaid draws parent edges " +
		"solid and blocker edges dotted, between shown issues only."
}

func (c *GraphCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Graph(board.GraphOpts{Issue: c.Issue, Closed: c.Closed, JSON: c.JSON, Mermaid: c.Mermaid})
}

type ShowCmd struct {
	Issue int  `arg:"" help:"Issue number."`
	JSON  bool `name:"json" help:"Print JSON, the same shape as one gh board graph --json entry."`
}

func (c *ShowCmd) Help() string {
	return "Works for closed and archived issues too, as long as they are on the board. Relation targets show their " +
		"Status when they are open board issues. A lock stays until another claim takes it over, so a closed issue " +
		"can still show one."
}

func (c *ShowCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Show(c.Issue, c.JSON)
}

type LinkCmd struct {
	Issue     int      `arg:"" help:"Issue number."`
	Parent    string   `placeholder:"ISSUE" help:"Parent issue, as a number or URL."`
	BlockedBy []string `placeholder:"ISSUES" help:"Blocking issues, as comma-separated numbers or URLs."`
}

func (c *LinkCmd) Help() string {
	return "Checks every target exists before writing, then adds each relation and prints it. A failure lists the " +
		"links still missing; one with no clear outcome exits 4."
}

func (c *LinkCmd) Run(e *env) error {
	a, err := e.app()
	if err != nil {
		return err
	}
	return a.Link(board.LinkOpts{Issue: c.Issue, Parent: c.Parent, BlockedBy: c.BlockedBy})
}

type DoctorCmd struct{}

func (c *DoctorCmd) Help() string {
	return "Checks the config, gh login and project scope, project write access, push permission, the remote's " +
		"fetch and push URLs, a dry-run push, the issue forms and the project's fields. Writes nothing."
}

func (c *DoctorCmd) Run(e *env) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return board.Doctor(dir, e.version, func(f string, a ...any) { fmt.Printf(f, a...) })
}

type SkillCmd struct{}

func (c *SkillCmd) Help() string {
	return "Prints a Markdown guide with SKILL.md frontmatter: the workflow, every command, exit codes, recovery steps " +
		"and this repo's board settings. Outside a configured checkout it prints the guide without repo settings."
}

func (c *SkillCmd) Run(e *env) error {
	var a *board.App
	if app, err := e.open(board.OpenLocal); err == nil {
		a = app
	}
	fmt.Print(skill(e.model, a))
	return nil
}
