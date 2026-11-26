package board

import (
	"encoding/json"
	"io"
	"net/http"
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
	if _, err := r.pickStatuses(fields); err != nil {
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
	_, err := r.pickStatuses([]gh.SelectField{{Name: "Status", Options: option("Todo", "Ready", "In progress")}})
	if err == nil || err.Error() != "pass --new with the Status option for new issues; options: Todo, Ready, In progress" {
		t.Fatalf("got %v", err)
	}
}

func TestInitValidatesFlagsBeforeAddingStatus(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts InitOpts
		want string
	}{
		{"required", InitOpts{Required: []string{"Typo"}}, "--required Typo"},
		{"sort", InitOpts{Sort: "Typo"}, `queue.sort "Typo"`},
		{"default", InitOpts{Defaults: []string{"Size=Typo"}}, `fields.Size.default "Typo"`},
		{"valid", InitOpts{Required: []string{"Size"}}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writes := 0
			stubGitHub(t, func(r *http.Request) (int, string) {
				switch r.Method + " " + r.URL.Path {
				case "GET /users/o":
					return 200, `{"type":"User"}`
				case "POST /graphql":
					var req struct {
						Query     string
						Variables struct {
							Field   string
							Options []gh.SelectOption
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					switch {
					case strings.Contains(req.Query, "repository(owner:"):
						return 200, `{"data":{"repository":{"projectsV2":{"nodes":[{"owner":{"login":"o"},"number":1}]},"issueTypes":{"nodes":[{"name":"Task","isEnabled":true}]}}}}`
					case strings.Contains(req.Query, "projectV2(number:"):
						return 200, `{"data":{"owner":{"projectV2":{"id":"p","fields":{"nodes":[{"id":"status","name":"Status","options":[{"id":"ready","name":"Ready"},{"id":"started","name":"In progress"}]},{"id":"size","name":"Size","options":[{"name":"S"}]}]}}}}}`
					case tt.want == "" && strings.Contains(req.Query, "updateProjectV2Field("):
						writes++
						opts := req.Variables.Options
						if req.Variables.Field != "status" || len(opts) != 3 || opts[0].ID != "ready" || opts[1].ID != "started" || opts[2].Name != "Triage" {
							t.Fatalf("unexpected field update: %+v", req.Variables)
						}
						return 200, `{"data":{"updateProjectV2Field":{"clientMutationId":null}}}`
					default:
						t.Fatalf("unexpected GraphQL request: %s", req.Query)
					}
				default:
					t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
				}
				return 0, ""
			})
			g := validationCheckout(t)
			opts := tt.opts
			opts.Remote, opts.Create, opts.New = "origin", true, "Triage"
			err := Init(g.Dir, opts, nil, io.Discard)
			if tt.want == "" {
				if err != nil || writes != 1 {
					t.Fatalf("init returned %v and wrote %d Status updates, want one successful write", err, writes)
				}
				cfg, err := config.Load(g.Dir)
				if err != nil || cfg.Statuses.New != "Triage" || !cfg.Fields["Size"].Required {
					t.Fatalf("unexpected config: %+v, %v", cfg, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want error containing %q", err, tt.want)
			}
			if _, err := os.Stat(filepath.Join(g.Dir, config.Path)); !os.IsNotExist(err) {
				t.Fatalf("unexpected stat result for config: %v", err)
			}
		})
	}
}
