package board

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/dan-online/gh-board/internal/config"
	"github.com/dan-online/gh-board/internal/gh"
	"github.com/dan-online/gh-board/internal/git"
)

// Asker prompts a person. Init runs without one for agents and scripts,
// and then every choice it cannot make from flags is an error naming the flag.
type Asker interface {
	Select(prompt, defaultValue string, options []string) (int, error)
	MultiSelect(prompt string, defaultValues, options []string) ([]int, error)
	Confirm(prompt string, defaultValue bool) (bool, error)
	Input(prompt, defaultValue string) (string, error)
}

type InitOpts struct {
	Remote      string
	Project     string // OWNER/NUMBER
	Create      bool   // allow creating the project and adding missing statuses
	Force       bool
	New         string
	Ready       string
	Claimed     string
	Fields      []string
	Required    []string
	Defaults    []string // NAME=VALUE
	Sort        string
	Forms       []string // LABEL=FILE
	DefaultForm string
	Types       []string
}

// The board a new project gets.
var (
	templateStatuses = []string{"Inbox", "Ready", "In progress", "In review", "Done"}
	templateRoles    = config.Statuses{New: "Inbox", Ready: "Ready", Claimed: "In progress"}
	templateFields   = map[string]config.Field{
		"Size":     {Options: []string{"S", "M", "L"}, Required: true},
		"Priority": {Options: []string{"Now", "Next"}, Default: "Next"},
	}
)

type initRun struct {
	o   InitOpts
	ask Asker
	out io.Writer
	gh  *gh.Client
	cfg *config.Config
}

// Init writes .github/board.yml for the checkout at dir.
func Init(dir string, o InitOpts, ask Asker, out io.Writer) error {
	g := git.Git{Dir: dir}
	root, err := g.Run("rev-parse", "--show-toplevel")
	if err != nil {
		return errors.New("gh board init runs inside a git checkout")
	}
	path := filepath.Join(root, config.Path)
	if _, err := os.Stat(path); err == nil && !o.Force {
		return fmt.Errorf("%s already exists; pass --force to replace it", config.Path)
	}
	url, err := g.Run("remote", "get-url", o.Remote)
	if err != nil {
		return fmt.Errorf("remote %s: %w", o.Remote, err)
	}
	repo, ok := git.RepoFromURL(url)
	if !ok {
		return fmt.Errorf("remote %s (%s) is not a github.com repository", o.Remote, url)
	}
	cfg := &config.Config{Repo: repo, Remote: o.Remote, APIVersion: gh.APIVersion, Worktrees: ".worktrees", Root: root}
	client, err := gh.New(cfg)
	if err != nil {
		return err
	}
	r := &initRun{o: o, ask: ask, out: out, gh: client, cfg: cfg}
	data, err := r.build()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Wrote %s for %s and project %s #%d.\nCommit it, then run gh board doctor.\n",
		config.Path, repo, cfg.Project.Owner, cfg.Project.Number)
	return nil
}

func (r *initRun) build() ([]byte, error) {
	info, err := r.gh.RepoInfo()
	if err != nil {
		return nil, err
	}
	// Settle every choice that needs no project before creating one.
	if r.cfg.Types = r.o.Types; len(r.cfg.Types) == 0 {
		r.cfg.Types = info.IssueTypes
	}
	if len(r.cfg.Types) == 0 {
		return nil, errors.New("the repo has no enabled issue types; pass --type for each type issues may use")
	}
	if err := r.pickForms(); err != nil {
		return nil, err
	}
	created, err := r.pickProject(info)
	if err != nil {
		return nil, err
	}
	projectID, fields, err := r.gh.SelectFields()
	if err != nil {
		return nil, err
	}
	var addStatuses func() error
	if created {
		if err := r.setUpNewProject(projectID, fields); err != nil {
			return nil, err
		}
	} else {
		if addStatuses, err = r.pickStatuses(fields); err != nil {
			return nil, err
		}
		if err := r.pickFields(fields); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(r.cfg); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	if _, err := config.Parse(data); err != nil {
		return nil, fmt.Errorf("generated config is invalid: %w", err)
	}
	if addStatuses != nil {
		if err := addStatuses(); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// consent asks for a write: a person confirms it, an agent passes --create.
func (r *initRun) consent(plan string) error {
	if r.o.Create {
		return nil
	}
	if r.ask == nil {
		return fmt.Errorf("init would %s. Pass --create to allow it", plan)
	}
	ok, err := r.ask.Confirm(strings.ToUpper(plan[:1])+plan[1:]+"?", true)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("stopped; nothing was written")
	}
	return nil
}

func (r *initRun) pickProject(info *gh.RepoInfo) (created bool, err error) {
	switch {
	case r.o.Project != "":
		owner, num, ok := strings.Cut(r.o.Project, "/")
		n, err := strconv.Atoi(num)
		if !ok || owner == "" || err != nil || n <= 0 {
			return false, exitf(ExitUsage, "--project takes OWNER/NUMBER, got %q", r.o.Project)
		}
		r.cfg.Project = config.Project{Owner: owner, Number: n}
		return false, nil
	case len(info.Projects) == 1:
		p := info.Projects[0]
		r.cfg.Project = config.Project{Owner: p.Owner, Number: p.Number}
		_, _ = fmt.Fprintf(r.out, "Using project %s #%d, %q, which is linked to %s.\n", p.Owner, p.Number, p.Title, r.cfg.Repo)
		return false, nil
	case len(info.Projects) > 1:
		var names []string
		for _, p := range info.Projects {
			names = append(names, fmt.Sprintf("%s/%d (%s)", p.Owner, p.Number, p.Title))
		}
		if r.ask == nil {
			return false, exitf(ExitUsage, "%s has several projects; pass --project with one of: %s", r.cfg.Repo, strings.Join(names, ", "))
		}
		i, err := r.ask.Select("Which project is the board?", names[0], names)
		if err != nil {
			return false, err
		}
		p := info.Projects[i]
		r.cfg.Project = config.Project{Owner: p.Owner, Number: p.Number}
		return false, nil
	}
	if len(r.o.Fields)+len(r.o.Required)+len(r.o.Defaults) > 0 || r.o.Sort != "" {
		return false, exitf(ExitUsage, "--field, --required, --default and --sort apply to an existing project; a new one gets Size (required) and Priority (default Next), ranked by Priority")
	}
	title := r.cfg.Name() + " board"
	plan := fmt.Sprintf("create project %q owned by %s and linked to %s, with Status %s, Size S, M or L, and Priority Now or Next",
		title, info.OwnerLogin, r.cfg.Repo, strings.Join(templateStatuses, ", "))
	if err := r.consent(plan); err != nil {
		return false, err
	}
	n, err := r.gh.CreateProject(info.OwnerID, info.ID, title)
	if err != nil {
		return false, fmt.Errorf("create project: %w", err)
	}
	r.cfg.Project = config.Project{Owner: info.OwnerLogin, Number: n}
	_, _ = fmt.Fprintf(r.out, "Created project %s #%d.\n", info.OwnerLogin, n)
	return true, nil
}

func (r *initRun) setUpNewProject(projectID string, fields []gh.SelectField) error {
	status := findField(fields, "Status")
	if status == nil {
		return errors.New("the new project has no Status field")
	}
	opts := make([]gh.SelectOption, len(templateStatuses))
	for i, s := range templateStatuses {
		opts[i] = gh.SelectOption{Name: s, Color: "GRAY"}
	}
	if err := r.gh.SetOptions(status.ID, opts); err != nil {
		return fmt.Errorf("set the Status options: %w", err)
	}
	for _, name := range []string{"Size", "Priority"} {
		if err := r.gh.CreateSelectField(projectID, name, templateFields[name].Options); err != nil {
			return fmt.Errorf("create the %s field: %w", name, err)
		}
	}
	r.cfg.Statuses = templateRoles
	r.cfg.Fields = templateFields
	r.cfg.Queue = &config.Queue{Sort: "Priority"}
	return nil
}

func findField(fields []gh.SelectField, name string) *gh.SelectField {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

func optionNames(f *gh.SelectField) []string {
	names := make([]string, len(f.Options))
	for i, o := range f.Options {
		names[i] = o.Name
	}
	return names
}

func (r *initRun) pickStatuses(fields []gh.SelectField) (func() error, error) {
	status := findField(fields, "Status")
	if status == nil {
		return nil, fmt.Errorf("project %s #%d has no Status field", r.cfg.Project.Owner, r.cfg.Project.Number)
	}
	options := optionNames(status)
	roles := []struct {
		flag, role, template string
		set                  *string
		given                string
	}{
		{"--new", "new issues", templateRoles.New, &r.cfg.Statuses.New, r.o.New},
		{"--ready", "issues ready to claim", templateRoles.Ready, &r.cfg.Statuses.Ready, r.o.Ready},
		{"--claimed", "claimed issues", templateRoles.Claimed, &r.cfg.Statuses.Claimed, r.o.Claimed},
	}
	var missing []string
	for _, role := range roles {
		switch {
		case role.given != "":
			*role.set = role.given
		case r.ask != nil:
			def := role.template
			if !slices.Contains(options, def) {
				def = options[0]
			}
			i, err := r.ask.Select("Status for "+role.role+"?", def, options)
			if err != nil {
				return nil, err
			}
			*role.set = options[i]
		case slices.Contains(options, role.template):
			*role.set = role.template
		default:
			return nil, exitf(ExitUsage, "pass %s with the Status option for %s; options: %s", role.flag, role.role, strings.Join(options, ", "))
		}
		if !slices.Contains(options, *role.set) && !slices.Contains(missing, *role.set) {
			missing = append(missing, *role.set)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	plan := fmt.Sprintf("add %s to the Status field of project %s #%d, keeping its options and their values",
		strings.Join(missing, ", "), r.cfg.Project.Owner, r.cfg.Project.Number)
	opts := slices.Clone(status.Options)
	for _, m := range missing {
		opts = append(opts, gh.SelectOption{Name: m, Color: "GRAY"})
	}
	return func() error {
		if err := r.consent(plan); err != nil {
			return err
		}
		return r.gh.SetOptions(status.ID, opts)
	}, nil
}

func (r *initRun) pickFields(fields []gh.SelectField) error {
	var candidates []string
	for _, f := range fields {
		if f.Name != "Status" {
			candidates = append(candidates, f.Name)
		}
	}
	chosen := r.o.Fields
	if len(chosen) == 0 && r.ask != nil && len(candidates) > 0 {
		picked, err := r.ask.MultiSelect("Which project fields should gh board set and check?", candidates, candidates)
		if err != nil {
			return err
		}
		for _, i := range picked {
			chosen = append(chosen, candidates[i])
		}
	} else if len(chosen) == 0 {
		chosen = candidates
	}
	required := r.o.Required
	if r.ask != nil && len(required) == 0 && len(chosen) > 0 {
		picked, err := r.ask.MultiSelect("Which fields must be set when filing?", nil, chosen)
		if err != nil {
			return err
		}
		for _, i := range picked {
			required = append(required, chosen[i])
		}
	}
	for _, name := range required {
		if !slices.Contains(chosen, name) {
			return exitf(ExitUsage, "--required %s: not one of the chosen fields: %s", name, strings.Join(chosen, ", "))
		}
	}
	r.cfg.Fields = map[string]config.Field{}
	for _, name := range chosen {
		f := findField(fields, name)
		if f == nil {
			return exitf(ExitUsage, "--field %s: no single-select field by that name; fields: %s", name, strings.Join(candidates, ", "))
		}
		r.cfg.Fields[name] = config.Field{Options: optionNames(f), Required: slices.Contains(required, name)}
	}
	for _, d := range r.o.Defaults {
		name, value, _ := strings.Cut(d, "=")
		f, ok := r.cfg.Fields[name]
		if !ok {
			return exitf(ExitUsage, "--default %s: not one of the chosen fields", d)
		}
		f.Default = value
		r.cfg.Fields[name] = f
	}
	sort := r.o.Sort
	if _, ok := r.cfg.Fields["Priority"]; sort == "" && ok {
		sort = "Priority"
	}
	if sort != "" {
		r.cfg.Queue = &config.Queue{Sort: sort}
	}
	return nil
}

func (r *initRun) pickForms() error {
	dir := filepath.Join(r.cfg.Root, ".github", "ISSUE_TEMPLATE")
	entries, _ := os.ReadDir(dir)
	var files []string
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if (ext == ".yml" || ext == ".yaml") && strings.TrimSuffix(e.Name(), ext) != "config" {
			files = append(files, e.Name())
		}
	}
	for _, f := range r.o.Forms {
		label, file, ok := strings.Cut(f, "=")
		if !ok || label == "" || !slices.Contains(files, file) {
			return exitf(ExitUsage, "--form takes LABEL=FILE with a file in .github/ISSUE_TEMPLATE, got %q; files: %s", f, strings.Join(files, ", "))
		}
		r.cfg.Forms = append(r.cfg.Forms, config.Form{Label: label, File: file})
	}
	def := r.o.DefaultForm
	switch {
	case def != "":
		if !slices.Contains(files, def) {
			return exitf(ExitUsage, "--default-form %s is not in .github/ISSUE_TEMPLATE; files: %s", def, strings.Join(files, ", "))
		}
	case len(r.o.Forms) > 0 || len(files) == 0:
	case r.ask != nil:
		var err error
		if def, err = r.askForms(files); err != nil {
			return err
		}
	case len(files) == 1:
		def = files[0]
	default:
		return exitf(ExitUsage, "pass --default-form FILE and --form LABEL=FILE for the issue forms in .github/ISSUE_TEMPLATE: %s", strings.Join(files, ", "))
	}
	if def != "" {
		r.cfg.Forms = append(r.cfg.Forms, config.Form{Default: true, File: def})
	}
	return nil
}

// askForms labels the forms and returns the default one, if any.
func (r *initRun) askForms(files []string) (string, error) {
	none := "none: ready checks only required fields"
	i, err := r.ask.Select("Which issue form checks issues without a matching label?", files[0], append(slices.Clone(files), none))
	if err != nil {
		return "", err
	}
	def := ""
	if i < len(files) {
		def = files[i]
	}
	for _, f := range files {
		if f == def {
			continue
		}
		label, err := r.ask.Input("Label that selects "+f+" (empty to skip)", "")
		if err != nil {
			return "", err
		}
		if label = strings.TrimSpace(label); label != "" {
			r.cfg.Forms = append(r.cfg.Forms, config.Form{Label: label, File: f})
		}
	}
	return def, nil
}
