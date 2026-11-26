package board

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dan-online/gh-board/internal/gh"
)

type FileOpts struct {
	Type      string
	Title     string
	BodyFile  string
	Fields    []string // NAME=VALUE
	Labels    []string
	Parent    string
	BlockedBy []string
}

// File creates an issue, adds it to the project and links its relations.
// Everything that can be checked is checked before the issue exists.
func (a *App) File(o FileOpts) error {
	if !slices.Contains(a.Cfg.Types, o.Type) {
		return exitf(ExitUsage, "--type must be one of %s", strings.Join(a.Cfg.Types, ", "))
	}
	if o.Title == "" {
		return exitf(ExitUsage, "--title is required")
	}
	body, err := os.ReadFile(o.BodyFile)
	if err != nil {
		return exitf(ExitUsage, "--body-file: %v", err)
	}
	values, err := a.fieldValues(o.Fields)
	if err != nil {
		return err
	}
	for _, l := range o.Labels {
		if err := a.GH.LabelExists(l); err != nil {
			return fmt.Errorf("label not found: %s", l)
		}
	}
	parentPath, blockers, err := a.relationTargets(o.Parent, o.BlockedBy)
	if err != nil {
		return err
	}
	if _, err := a.GH.Fields(); err != nil {
		return err
	}

	issue, err := a.GH.CreateIssue(o.Title, string(body), o.Type, o.Labels)
	if err != nil {
		if gh.Definite(err) {
			return fmt.Errorf("create issue: %w", err)
		}
		return exitf(ExitIncomplete, "create issue: %v\nThe issue may exist; look for it before filing again", err)
	}
	a.printf("%s\n", issue.HTMLURL)

	var itemID int64
	steps := []step{
		{"add to project", func() (err error) { itemID, err = a.GH.AddItem(issue); return }},
		{"set " + describe(values), func() error { return a.GH.SetFields(itemID, values) }},
	}
	steps = append(steps, a.relationSteps(issue, o.Parent, parentPath, o.BlockedBy, blockers)...)
	for i, s := range steps {
		if err := s.run(); err != nil {
			rest := remaining(steps[i+1:])
			if !gh.Definite(err) {
				return exitf(ExitIncomplete, "%s: %v\nIssue #%d exists. This write may have happened: check it before repeating it. Still missing after it: %s", s.name, err, issue.Number, rest)
			}
			return fmt.Errorf("%s: %w\nIssue #%d exists, but these writes are missing: %s; then %s", s.name, err, issue.Number, s.name, rest)
		}
	}
	return nil
}

// fieldValues checks --field pairs against the config, applies defaults,
// requires required fields and adds the new status.
func (a *App) fieldValues(pairs []string) (map[string]string, error) {
	values := map[string]string{}
	for _, p := range pairs {
		name, value, ok := strings.Cut(p, "=")
		f, known := a.Cfg.Fields[name]
		switch {
		case !ok || value == "":
			return nil, exitf(ExitUsage, "--field takes NAME=VALUE, got %q", p)
		case !known:
			return nil, exitf(ExitUsage, "--field %s: not a field in board.yml; fields: %s", name, strings.Join(a.Cfg.FieldNames(), ", "))
		case !slices.Contains(f.Options, value):
			return nil, exitf(ExitUsage, "--field %s must be one of %s", name, strings.Join(f.Options, ", "))
		}
		values[name] = value
	}
	for _, name := range a.Cfg.FieldNames() {
		f := a.Cfg.Fields[name]
		if _, set := values[name]; !set {
			if f.Default != "" {
				values[name] = f.Default
			} else if f.Required {
				return nil, exitf(ExitUsage, "--field %s=VALUE is required (%s)", name, strings.Join(f.Options, ", "))
			}
		}
	}
	values["Status"] = a.Cfg.Statuses.New
	return values, nil
}

type step struct {
	name string
	run  func() error
}

// remaining names the steps not yet run, for a failure message.
func remaining(steps []step) string {
	if len(steps) == 0 {
		return "nothing else"
	}
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.name
	}
	return strings.Join(names, "; ")
}

// relationTargets checks a parent and blockers, given as numbers or URLs, before anything is written.
func (a *App) relationTargets(parent string, blockedBy []string) (parentPath string, blockers []*gh.Issue, err error) {
	if parent != "" {
		if parentPath, err = a.GH.IssuePath(parent); err != nil {
			return "", nil, exitf(ExitUsage, "--parent: %v", err)
		}
		if _, err := a.GH.IssueAt(parentPath); err != nil {
			return "", nil, fmt.Errorf("parent issue not found: %s", parent)
		}
	}
	for _, ref := range blockedBy {
		path, err := a.GH.IssuePath(ref)
		if err != nil {
			return "", nil, exitf(ExitUsage, "--blocked-by: %v", err)
		}
		b, err := a.GH.IssueAt(path)
		if err != nil {
			return "", nil, fmt.Errorf("blocking issue not found: %s", ref)
		}
		blockers = append(blockers, b)
	}
	return parentPath, blockers, nil
}

func (a *App) relationSteps(issue *gh.Issue, parent, parentPath string, blockedBy []string, blockers []*gh.Issue) []step {
	var steps []step
	if parentPath != "" {
		steps = append(steps, step{"add as sub-issue of " + parent, func() error { return a.GH.AddSubIssue(parentPath, issue) }})
	}
	for i, b := range blockers {
		steps = append(steps, step{"add blocked by " + blockedBy[i], func() error { return a.GH.AddBlockedBy(issue, b) }})
	}
	return steps
}

func describe(values map[string]string) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(values)) {
		parts = append(parts, k+"="+values[k])
	}
	return strings.Join(parts, ", ")
}
