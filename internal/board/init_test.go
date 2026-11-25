package board

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dan-online/gh-board/internal/config"
	"github.com/dan-online/gh-board/internal/gh"
)

// scripted answers each prompt from a queue and records the defaults it was offered.
type scripted struct {
	selects  []string // option to pick, "" takes the default
	multi    [][]int
	inputs   []string
	defaults []string
}

func (s *scripted) Select(_, def string, opts []string) (int, error) {
	s.defaults = append(s.defaults, def)
	pick := s.selects[0]
	s.selects = s.selects[1:]
	if pick == "" {
		pick = def
	}
	return slices.Index(opts, pick), nil
}

func (s *scripted) MultiSelect(string, []string, []string) ([]int, error) {
	m := s.multi[0]
	s.multi = s.multi[1:]
	return m, nil
}

func (s *scripted) Confirm(string, bool) (bool, error) { return true, nil }

func (s *scripted) Input(string, string) (string, error) {
	in := s.inputs[0]
	s.inputs = s.inputs[1:]
	return in, nil
}

func option(names ...string) []gh.SelectOption {
	opts := make([]gh.SelectOption, len(names))
	for i, n := range names {
		opts[i] = gh.SelectOption{ID: n, Name: n}
	}
	return opts
}

func TestInitAsksForStatusesFieldsAndForms(t *testing.T) {
	root := t.TempDir()
	forms := filepath.Join(root, ".github", "ISSUE_TEMPLATE")
	if err := os.MkdirAll(forms, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bug.yml", "work.yml", "config.yml"} {
		if err := os.WriteFile(filepath.Join(forms, f), []byte("body: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ask := &scripted{
		selects: []string{"Todo", "", "Doing", "work.yml"},
		multi:   [][]int{{1}, {0}},
		inputs:  []string{"bug"},
	}
	r := &initRun{ask: ask, out: io.Discard, cfg: &config.Config{Root: root}}
	fields := []gh.SelectField{
		{Name: "Status", Options: option("Todo", "Ready", "Doing", "Done")},
		{Name: "Effort", Options: option("1", "2")},
		{Name: "Priority", Options: option("High", "Low")},
	}
	if err := r.pickStatuses(fields); err != nil {
		t.Fatal(err)
	}
	if err := r.pickFields(fields); err != nil {
		t.Fatal(err)
	}
	if err := r.pickForms(); err != nil {
		t.Fatal(err)
	}
	// Ready matches the template, so it is offered as the default; Inbox and In progress are absent.
	if want := []string{"Todo", "Ready", "Todo", "bug.yml"}; !slices.Equal(ask.defaults, want) {
		t.Errorf("defaults offered %v, want %v", ask.defaults, want)
	}
	if want := (config.Statuses{New: "Todo", Ready: "Ready", Claimed: "Doing"}); r.cfg.Statuses != want {
		t.Errorf("statuses %+v", r.cfg.Statuses)
	}
	if f, ok := r.cfg.Fields["Priority"]; !ok || !f.Required || len(r.cfg.Fields) != 1 {
		t.Errorf("fields %+v", r.cfg.Fields)
	}
	if r.cfg.Queue == nil || r.cfg.Queue.Sort != "Priority" {
		t.Errorf("queue %+v", r.cfg.Queue)
	}
	want := []config.Form{{Label: "bug", File: "bug.yml"}, {Default: true, File: "work.yml"}}
	if !reflect.DeepEqual(r.cfg.Forms, want) {
		t.Errorf("forms %+v", r.cfg.Forms)
	}
}

func TestInitRejectsUnknownRequiredField(t *testing.T) {
	r := &initRun{out: io.Discard, cfg: &config.Config{}, o: InitOpts{Required: []string{"Szie"}}}
	err := r.pickFields([]gh.SelectField{{Name: "Status"}, {Name: "Size", Options: option("S")}})
	if err == nil || err.Error() != "--required Szie: not one of the chosen fields: Size" {
		t.Fatalf("got %v", err)
	}
}

func TestInitRefusesFieldFlagsBeforeCreatingAProject(t *testing.T) {
	// No gh client: reaching CreateProject would panic.
	r := &initRun{out: io.Discard, cfg: &config.Config{Repo: "o/r"}, o: InitOpts{Create: true, Required: []string{"Effort"}}}
	_, err := r.pickProject(&gh.RepoInfo{OwnerLogin: "o"})
	if err == nil || !strings.HasPrefix(err.Error(), "--field, --required, --default and --sort apply to an existing project") {
		t.Fatalf("got %v", err)
	}
}

func TestInitWithoutTerminalNamesTheFlag(t *testing.T) {
	r := &initRun{out: io.Discard, cfg: &config.Config{}}
	err := r.pickStatuses([]gh.SelectField{{Name: "Status", Options: option("Todo", "Ready", "In progress")}})
	if err == nil || err.Error() != "pass --new with the Status option for new issues; options: Todo, Ready, In progress" {
		t.Fatalf("got %v", err)
	}
}
