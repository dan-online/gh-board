package board

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/dan-online/gh-board/internal/config"
)

// Doctor checks one thing per line and fails if any check fails.
// It writes nothing to GitHub; the push check is a dry run.
func Doctor(dir, version string, out func(format string, a ...any)) error {
	failed := 0
	check := func(name string, err error, detail string) {
		if err != nil {
			failed++
			out("FAIL %s: %v\n", name, err)
			return
		}
		out("ok   %s%s\n", name, detail)
	}
	out("gh-board %s, %s\n", version, ghVersion())

	a, err := Open(dir, version)
	check("config "+config.Path, err, "")
	if err != nil {
		return errors.New("doctor found problems")
	}

	login, scopes, err := a.GH.Viewer()
	check("gh login", err, ": "+login)
	switch {
	case err != nil:
	case scopes == nil:
		out("?    token scopes: not reported by this token type; the project checks below decide\n")
	default:
		var missing error
		if !slices.Contains(scopes, "project") {
			missing = fmt.Errorf("token lacks the project scope; run: gh auth refresh -s project")
		}
		check("token scope project", missing, "")
	}

	canUpdate, err := a.GH.ViewerCanUpdateProject()
	if err == nil && !canUpdate {
		err = fmt.Errorf("%s cannot update project %s #%d", login, a.Cfg.Project.Owner, a.Cfg.Project.Number)
	}
	check("project write access", err, fmt.Sprintf(": %s #%d", a.Cfg.Project.Owner, a.Cfg.Project.Number))

	repo, err := a.GH.Repo()
	if err == nil && !repo.Permissions.Push {
		err = fmt.Errorf("%s has no push access to %s", login, a.Cfg.Repo)
	}
	check("repo push permission", err, ": "+a.Cfg.Repo)

	check("remote "+a.Cfg.Remote+" fetch and push URLs", a.checkRemote(), "")
	_, err = a.Git.Run("push", "--dry-run", "--porcelain", a.Cfg.Remote, "HEAD:refs/heads/gh-board-doctor-probe")
	check("git push over "+a.Cfg.Remote+" (dry run)", err, "")

	for _, f := range a.Cfg.Forms {
		_, err := os.Stat(a.Cfg.FormPath(&f))
		check("issue form "+f.File, err, "")
	}
	for _, name := range append([]string{"Status"}, a.Cfg.FieldNames()...) {
		field, err := a.GH.Field(name)
		if err == nil {
			want := a.Cfg.Fields[name].Options
			if name == "Status" {
				want = []string{a.Cfg.Statuses.New, a.Cfg.Statuses.Ready, a.Cfg.Statuses.Claimed}
			}
			options := field.OptionNames()
			var absent []string
			for _, o := range want {
				if !slices.Contains(options, o) {
					absent = append(absent, o)
				}
			}
			if len(absent) > 0 {
				err = fmt.Errorf("project field %s has no option %s", name, strings.Join(absent, ", "))
			}
		}
		check("project field "+name, err, "")
	}
	if failed > 0 {
		return fmt.Errorf("doctor found %d problem(s)", failed)
	}
	return nil
}

func ghVersion() string {
	path := os.Getenv("GH_PATH")
	if path == "" {
		path = "gh"
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "gh not found"
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return first
}
