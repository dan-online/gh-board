package board

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/dan-online/gh-board/internal/config"
	"github.com/dan-online/gh-board/internal/gh"
	"github.com/dan-online/gh-board/internal/git"
)

// fakeAPI scripts the GitHub side of a claim.
type fakeAPI struct {
	createErr error
	createPR  *gh.PR // PR that exists after CreatePR, even when it errs
	setErr    error
	onSet     func() // runs inside SetFields, before it fails
	closeErr  error
	closed    []int
	statuses  []string
	stack     *gh.Stack // what StackFor returns for the claim PR
	stackErr  error     // returned by CreateStack and AddToStack
	stacked   [][]int
}

func (f *fakeAPI) StackFor(int) (*gh.Stack, error) { return f.stack, nil }
func (f *fakeAPI) CreateStack(prs ...int) (*gh.Stack, error) {
	f.stacked = append(f.stacked, prs)
	return f.stack, f.stackErr
}
func (f *fakeAPI) AddToStack(_ int, prs ...int) (*gh.Stack, error) {
	f.stacked = append(f.stacked, prs)
	return f.stack, f.stackErr
}

func (f *fakeAPI) CreatePR(_, head, _, body string) (*gh.PR, error) {
	if f.createPR != nil {
		f.createPR.Body = body
		f.createPR.Head.Ref = head
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createPR, nil
}
func (f *fakeAPI) ClosePR(n int) error { f.closed = append(f.closed, n); return f.closeErr }
func (f *fakeAPI) PRsForBranch(string) ([]gh.PR, error) {
	if f.createPR == nil {
		return nil, nil
	}
	return []gh.PR{*f.createPR}, nil
}
func (f *fakeAPI) SetFields(_ int64, v map[string]string) error {
	f.statuses = append(f.statuses, v["Status"])
	if f.onSet != nil {
		f.onSet()
	}
	return f.setErr
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setup returns a claim whose lock is already pushed to a local bare remote.
func setup(t *testing.T, api *fakeAPI) (*claim, string) {
	t.Helper()
	tmp := t.TempDir()
	remote, main := filepath.Join(tmp, "remote.git"), filepath.Join(tmp, "main")
	run(t, tmp, "init", "-q", "--bare", "-b", "main", remote)
	run(t, tmp, "init", "-q", "-b", "main", main)
	run(t, main, "config", "user.name", "t")
	run(t, main, "config", "user.email", "t@example.com")
	run(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, main, "remote", "add", "origin", remote)
	run(t, main, "push", "-q", "origin", "main")
	run(t, main, "fetch", "-q", "origin")

	var out bytes.Buffer
	a := &App{Cfg: &config.Config{Remote: "origin", Statuses: config.Statuses{Ready: "Ready", Claimed: "In progress"}},
		Git: git.Git{Dir: main}, Out: &out}
	c := &claim{a: a, gh: api, number: 7, branch: "7-thing", worktree: filepath.Join(main, ".worktrees", "7-thing")}
	c.lock = lock{Ref: lockRef(7), Attempt: "abc123", Branch: c.branch}
	sha, err := a.claimCommit(7, run(t, main, "rev-parse", "origin/main"), c.branch, c.lock.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	c.lock.SHA = sha
	if res, _, err := a.pushRef(c.lock.Ref, sha, ""); res != pushed {
		t.Fatalf("lock push: %v %v", res, err)
	}
	return c, remote
}

func remoteHas(t *testing.T, remote, ref string) string {
	t.Helper()
	out, _ := exec.Command("git", "--git-dir", remote, "rev-parse", "--verify", "--quiet", ref).Output()
	return strings.TrimSpace(string(out))
}

var errTimeout = errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")

func TestClaimKeepsEverythingWhenPRCreateHasNoAnswer(t *testing.T) {
	c, remote := setup(t, &fakeAPI{createErr: errTimeout})
	err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil)
	var be *Error
	if !errors.As(err, &be) || be.Code != ExitIncomplete {
		t.Fatalf("got %v, want exit %d", err, ExitIncomplete)
	}
	if remoteHas(t, remote, c.lock.Ref) != c.lock.SHA || remoteHas(t, remote, "refs/heads/"+c.branch) != c.lock.SHA {
		t.Fatal("lock or branch was removed after an unknown outcome")
	}
	if _, err := os.Stat(c.worktree); err != nil {
		t.Fatal("worktree was removed after an unknown outcome")
	}
}

func TestClaimFindsThePRWhoseCreateAnswerWasLost(t *testing.T) {
	api := &fakeAPI{createErr: errTimeout, createPR: &gh.PR{Number: 12, HTMLURL: "https://github.com/o/r/pull/12"}}
	c, _ := setup(t, api)
	if err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil); err != nil {
		t.Fatal(err)
	}
	if out := c.a.Out.(*bytes.Buffer).String(); !strings.Contains(out, "pull/12") {
		t.Fatalf("output %q", out)
	}
	if len(api.statuses) != 1 || api.statuses[0] != "In progress" {
		t.Fatalf("statuses %v", api.statuses)
	}
}

func TestClaimRollsBackAndReleasesOnDefiniteFailure(t *testing.T) {
	c, remote := setup(t, &fakeAPI{createErr: &api.HTTPError{StatusCode: 422, Message: "Validation Failed"}})
	err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil)
	if err == nil || strings.Contains(err.Error(), "Cleanup could not") {
		t.Fatalf("got %v", err)
	}
	if remoteHas(t, remote, c.lock.Ref) != "" || remoteHas(t, remote, "refs/heads/"+c.branch) != "" {
		t.Fatal("lock or branch left on the remote")
	}
	if _, err := os.Stat(c.worktree); !os.IsNotExist(err) {
		t.Fatal("worktree left behind")
	}
	if out, _ := exec.Command("git", "-C", c.a.Git.Dir, "branch", "--list", c.branch).Output(); len(out) > 0 {
		t.Fatal("local branch left behind")
	}
}

func TestClaimKeepsTheLockWhenCleanupFails(t *testing.T) {
	api := &fakeAPI{createPR: &gh.PR{Number: 12}, setErr: &api.HTTPError{StatusCode: 403}, closeErr: errors.New("boom")}
	c, remote := setup(t, api)
	err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil)
	if err == nil || !strings.Contains(err.Error(), "close PR #12") || !strings.Contains(err.Error(), "gh board release 7 --sha "+c.lock.SHA) {
		t.Fatalf("got %v", err)
	}
	if remoteHas(t, remote, c.lock.Ref) != c.lock.SHA {
		t.Fatal("lock released although the PR is still open")
	}
}

func TestClaimLeavesAnotherAttemptsBranchAlone(t *testing.T) {
	c, remote := setup(t, &fakeAPI{createPR: &gh.PR{Number: 12}})
	main := c.a.Git.Dir
	run(t, main, "commit", "-q", "--allow-empty", "-m", "theirs")
	theirs := run(t, main, "rev-parse", "HEAD")
	run(t, main, "push", "-q", "origin", "HEAD:refs/heads/"+c.branch)
	run(t, main, "reset", "-q", "--soft", "HEAD~1")

	err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil)
	if err == nil {
		t.Fatal("claimed over an existing branch")
	}
	if remoteHas(t, remote, "refs/heads/"+c.branch) != theirs {
		t.Fatal("the other attempt's branch changed")
	}
	if remoteHas(t, remote, c.lock.Ref) != "" {
		t.Fatal("lock kept after a definite failure with nothing left to undo")
	}
}

func TestClaimKeepsPRAndLockWhenItsBranchMovedDuringRollback(t *testing.T) {
	api := &fakeAPI{createPR: &gh.PR{Number: 12}, setErr: &api.HTTPError{StatusCode: 403}}
	c, remote := setup(t, api)
	api.onSet = func() {
		wt := c.worktree
		run(t, wt, "commit", "-q", "--allow-empty", "-m", "theirs")
		run(t, wt, "push", "-q", "origin", "HEAD:refs/heads/"+c.branch)
	}
	err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil)
	if err == nil || !strings.Contains(err.Error(), "delete remote branch") {
		t.Fatalf("got %v", err)
	}
	if len(api.closed) != 0 {
		t.Fatal("PR closed while the attempt's branch could not be removed; the lock would look takeable")
	}
	if remoteHas(t, remote, c.lock.Ref) != c.lock.SHA {
		t.Fatal("lock released with leftovers on the remote")
	}
}

func TestClaimKeepsAnExistingLocalBranch(t *testing.T) {
	c, _ := setup(t, &fakeAPI{createPR: &gh.PR{Number: 12}})
	main := c.a.Git.Dir
	run(t, main, "branch", c.branch)
	mine := run(t, main, "rev-parse", c.branch)
	if err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil); err == nil {
		t.Fatal("claimed over an existing local branch")
	}
	if got := run(t, main, "rev-parse", c.branch); got != mine {
		t.Fatalf("local branch changed or deleted: %s", got)
	}
}

func TestClaimDoesNotOwnABranchSomeoneElseCreatedAtItsCommit(t *testing.T) {
	c, remote := setup(t, &fakeAPI{createPR: &gh.PR{Number: 12}})
	run(t, c.a.Git.Dir, "push", "-q", "origin", c.lock.SHA+":refs/heads/"+c.branch)
	if err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", nil); err == nil {
		t.Fatal("claimed although the branch push was refused")
	}
	if remoteHas(t, remote, "refs/heads/"+c.branch) != c.lock.SHA {
		t.Fatal("deleted a branch this attempt did not create")
	}
}

func stackOf(prs ...int) *gh.Stack {
	s := &gh.Stack{Number: 1, Open: true}
	for _, p := range prs {
		s.PullRequests = append(s.PullRequests, gh.PR{Number: p})
	}
	return s
}

func parentPR() *parent {
	p := &parent{issue: 6, pr: &gh.PR{Number: 11}}
	p.pr.Head.Ref = "6-base"
	return p
}

func TestStackedClaimRegistersOnTopOfItsParent(t *testing.T) {
	api := &fakeAPI{createPR: &gh.PR{Number: 12}, stack: stackOf(11, 12)}
	c, _ := setup(t, api)
	if err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", parentPR()); err != nil {
		t.Fatal(err)
	}
	if len(api.stacked) != 1 || api.stacked[0][0] != 11 || api.stacked[0][1] != 12 {
		t.Fatalf("stack calls %v", api.stacked)
	}
}

func TestStackedClaimRollsBackWhenTheStackRefusedIt(t *testing.T) {
	api := &fakeAPI{createPR: &gh.PR{Number: 12}, stackErr: &api.HTTPError{StatusCode: 422}}
	c, remote := setup(t, api)
	if err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", parentPR()); err == nil {
		t.Fatal("claimed although the stack refused the PR")
	}
	if len(api.closed) != 1 || remoteHas(t, remote, c.lock.Ref) != "" {
		t.Fatalf("closed %v, lock %q", api.closed, remoteHas(t, remote, c.lock.Ref))
	}
}

func TestStackedClaimContinuesWhenTheStackAnswerWasLost(t *testing.T) {
	api := &fakeAPI{createPR: &gh.PR{Number: 12}, stack: stackOf(11, 12), stackErr: errTimeout}
	c, _ := setup(t, api)
	if err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", parentPR()); err != nil {
		t.Fatal(err)
	}
}

func TestStackedClaimNeverClosesARegisteredPR(t *testing.T) {
	api := &fakeAPI{createPR: &gh.PR{Number: 12}, stack: stackOf(11, 12), setErr: &api.HTTPError{StatusCode: 403}}
	c, remote := setup(t, api)
	err := c.finish(ClaimOpts{Number: 7, Title: "feat: x"}, &gh.Item{}, "main", parentPR())
	var be *Error
	if !errors.As(err, &be) || be.Code != ExitIncomplete {
		t.Fatalf("got %v", err)
	}
	if len(api.closed) != 0 || remoteHas(t, remote, c.lock.Ref) != c.lock.SHA {
		t.Fatalf("closed %v; a stacked PR's claim must stay", api.closed)
	}
}
