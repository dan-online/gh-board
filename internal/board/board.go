// Package board implements the gh board commands.
package board

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dan-online/gh-board/internal/config"
	"github.com/dan-online/gh-board/internal/gh"
	"github.com/dan-online/gh-board/internal/git"
)

// Exit codes beyond 0 and 1.
const (
	ExitUsage      = 2 // bad arguments
	ExitHeld       = 3 // another claim holds the issue
	ExitIncomplete = 4 // a write's outcome is unknown; run gh board check
)

// Error carries an exit code.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func exitf(code int, format string, a ...any) error {
	return &Error{Code: code, Err: fmt.Errorf(format, a...)}
}

type App struct {
	Cfg     *config.Config
	GH      *gh.Client
	Git     git.Git
	Out     io.Writer
	Err     io.Writer // notes that are not command output
	Version string
}

// Open loads the board with a GitHub client.
func Open(dir, version string) (*App, error) {
	a, err := OpenLocal(dir, version)
	if err != nil {
		return nil, err
	}
	a.GH, err = gh.New(a.Cfg)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// OpenLocal loads the config from the checkout that holds dir without a GitHub client.
func OpenLocal(dir, version string) (*App, error) {
	g := git.Git{Dir: dir}
	root, err := g.Run("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("gh board runs inside a git checkout")
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	if err := cfg.CheckVersion(version); err != nil {
		return nil, err
	}
	return &App{Cfg: cfg, Git: git.Git{Dir: root}, Out: os.Stdout, Err: os.Stderr, Version: version}, nil
}

func (a *App) printf(format string, args ...any) { _, _ = fmt.Fprintf(a.Out, format, args...) }

func (a *App) notef(format string, args ...any) {
	if a.Err != nil {
		_, _ = fmt.Fprintf(a.Err, format, args...)
	}
}

// checkRemote fails unless the remote's fetch and push URLs both point at the board's repo.
func (a *App) checkRemote() error {
	for _, args := range [][]string{{"remote", "get-url", a.Cfg.Remote}, {"remote", "get-url", "--push", a.Cfg.Remote}} {
		u, err := a.Git.Run(args...)
		if err != nil {
			return fmt.Errorf("remote %s: %w", a.Cfg.Remote, err)
		}
		if repo, ok := git.RepoFromURL(u); !ok || !strings.EqualFold(repo, a.Cfg.Repo) {
			return fmt.Errorf("remote %s points at %s, not %s", a.Cfg.Remote, u, a.Cfg.Repo)
		}
	}
	return nil
}

// mainCheckout is the main working tree, even when run from a linked worktree.
func (a *App) mainCheckout() (string, error) {
	common, err := a.Git.Run("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Dir(common), nil
}

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	nonSlug     = regexp.MustCompile(`[^a-z0-9]+`)
)

// Slugify turns an issue title into a branch slug.
func Slugify(title string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
}

func (a *App) item(number int) (*gh.Issue, *gh.Item, error) {
	issue, err := a.GH.Issue(number)
	if err != nil {
		return nil, nil, err
	}
	item, err := a.GH.FindItem(issue)
	if err == nil {
		return issue, item, nil
	}
	if !errors.Is(err, gh.ErrNotOnProject) {
		return nil, nil, err
	}
	// Project search can miss archived or not-yet-indexed cards.
	items, scanErr := a.GH.Items()
	if scanErr != nil {
		return nil, nil, scanErr
	}
	for i := range items {
		if items[i].Issue.Number == number {
			return issue, &items[i], nil
		}
	}
	return nil, nil, err
}
