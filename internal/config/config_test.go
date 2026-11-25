package config

import (
	"strings"
	"testing"
)

const uptempo = `
repo: uptempohq/uptempo
remote: origin
project: { owner: uptempohq, number: 1 }
api_version: "2026-03-10"
min_version: v1.0.0
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
`

func TestParseUptempo(t *testing.T) {
	c, err := Parse([]byte(uptempo))
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner() != "uptempohq" || c.Name() != "uptempo" {
		t.Fatalf("repo split: %q %q", c.Owner(), c.Name())
	}
	if f := c.FormFor([]string{"bug", "decision"}); f == nil || f.File != "decision.yml" {
		t.Fatalf("decision label picked %+v", f)
	}
	if f := c.FormFor(nil); f == nil || f.File != "work.yml" {
		t.Fatalf("default form picked %+v", f)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]struct {
		edit func(string) string
		want string
	}{
		"unknown key":     {func(s string) string { return s + "colour: red\n" }, "colour"},
		"missing repo":    {func(s string) string { return strings.Replace(s, "repo: uptempohq/uptempo\n", "", 1) }, "repo is required"},
		"bad repo":        {func(s string) string { return strings.Replace(s, "uptempohq/uptempo", "uptempo", 1) }, "OWNER/NAME"},
		"bad default":     {func(s string) string { return strings.Replace(s, "default: Next", "default: Later", 1) }, "fields.Priority.default"},
		"bad require":     {func(s string) string { return strings.Replace(s, "Size: [S, M] }", "Size: [XL] }", 1) }, "forms[1].require.Size"},
		"unknown sort":    {func(s string) string { return strings.Replace(s, "sort: Priority", "sort: Effort", 1) }, "queue.sort"},
		"two defaults":    {func(s string) string { return strings.Replace(s, "label: decision,", "default: true,", 1) }, "more than one default"},
		"bad min_version": {func(s string) string { return strings.Replace(s, "v1.0.0", "1.0", 1) }, "min_version"},
		"status in fields": {func(s string) string {
			return strings.Replace(s, "fields:\n", "fields:\n  Status: { options: [A] }\n", 1)
		}, "fields.Status"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.edit(uptempo)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestCheckVersion(t *testing.T) {
	c, err := Parse([]byte(uptempo))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CheckVersion("v0.9.3"); err == nil {
		t.Fatal("v0.9.3 passed min_version v1.0.0")
	}
	for _, v := range []string{"v1.0.0", "v1.2.0", "(devel)"} {
		if err := c.CheckVersion(v); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
}
