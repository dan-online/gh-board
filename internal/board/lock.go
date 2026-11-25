package board

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dan-online/gh-board/internal/gh"
	"github.com/dan-online/gh-board/internal/git"
)

// A lock is the ref refs/claims/N. Its commit names the attempt that holds it.
type lock struct {
	Ref     string
	SHA     string
	Attempt string
	Branch  string
	Host    string
	Time    time.Time
}

func lockRef(n int) string { return "refs/claims/" + strconv.Itoa(n) }

func newAttempt() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// remoteRefs lists refs on the remote matching pattern, by name.
func (a *App) remoteRefs(pattern string) (map[string]string, error) {
	out, err := a.Git.Run("ls-remote", a.Cfg.Remote, pattern)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ref, ok := strings.Cut(line, "\t"); ok {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// remoteRef reads one ref; "" means it does not exist.
func (a *App) remoteRef(ref string) (string, error) {
	refs, err := a.remoteRefs(ref)
	return refs[ref], err
}

// readLock returns the issue's lock, or nil when there is none.
func (a *App) readLock(n int) (*lock, error) {
	sha, err := a.remoteRef(lockRef(n))
	if err != nil || sha == "" {
		return nil, err
	}
	return a.describeLock(lockRef(n), sha)
}

func (a *App) describeLock(ref, sha string) (*lock, error) {
	c, err := a.GH.Commit(sha)
	if err != nil {
		return nil, fmt.Errorf("read lock %s at %s: %w", ref, sha, err)
	}
	l := &lock{Ref: ref, SHA: sha, Time: c.Committer.Date}
	cmd := exec.Command("git", "interpret-trailers", "--parse")
	cmd.Stdin = strings.NewReader(c.Message)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, _ := strings.Cut(line, ": ")
		switch key {
		case "Board-Attempt":
			l.Attempt = value
		case "Board-Branch":
			l.Branch = value
		case "Board-Host":
			l.Host = value
		}
	}
	return l, nil
}

func (l *lock) String() string {
	return fmt.Sprintf("branch %s from %s, attempt %s, %s old", l.Branch, l.Host, l.Attempt, time.Since(l.Time).Round(time.Second))
}

// attemptMarker goes in the claim PR's body, so the PR can be tied to its lock.
func attemptMarker(attempt string) string { return "<!-- board-attempt: " + attempt + " -->" }

// lockPR finds the PR that the lock's attempt opened. Nil means none was found,
// which does not prove none exists.
func (a *App) lockPR(l *lock) (*gh.PR, error) {
	prs, err := a.GH.PRsForBranch(l.Branch)
	if err != nil {
		return nil, err
	}
	return attemptPR(prs, l.Attempt), nil
}

// attemptPR picks the PR whose body carries the attempt's marker. A PR on a
// reused branch name from another attempt does not count.
func attemptPR(prs []gh.PR, attempt string) *gh.PR {
	marker := attemptMarker(attempt)
	for i := range prs {
		if strings.Contains(prs[i].Body, marker) {
			return &prs[i]
		}
	}
	return nil
}

// takeable reports whether a lock's claim has finished: its PR is merged or closed.
func (a *App) takeable(l *lock) (bool, *gh.PR, error) {
	pr, err := a.lockPR(l)
	if err != nil || pr == nil {
		return false, pr, err
	}
	return pr.State == "closed", pr, nil
}

type pushResult int

const (
	pushed   pushResult = iota // the remote holds our SHA
	rejected                   // the remote refused; it holds something else
	unknown                    // no answer we can trust
)

// pushRef sets ref to sha only while the remote still holds expect ("" for absent;
// sha "" deletes), then reads the ref back. The read-back, not the exit code,
// decides the result, and now is what the remote holds afterwards.
func (a *App) pushRef(ref, sha, expect string) (pushResult, string, error) {
	return a.push(a.Git, ref, sha, expect, sha+":"+ref)
}

// push runs a leased push of src from checkout g and reads ref back.
func (a *App) push(g git.Git, ref, sha, expect, src string, opts ...string) (res pushResult, now string, err error) {
	args := append([]string{"push", "--porcelain", "--force-with-lease=" + ref + ":" + expect}, opts...)
	out, pushErr := g.Run(append(args, a.Cfg.Remote, src)...)
	refused := false
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Split(line, "\t"); len(fields) >= 2 && strings.HasSuffix(fields[1], ":"+ref) {
			// "=" means the ref already held sha before this push. Git reports it
			// without checking the lease, so it is someone else's ref, not ours.
			refused = strings.HasPrefix(line, "!") || strings.HasPrefix(line, "=")
		}
	}
	now, err = a.remoteRef(ref)
	switch {
	case refused:
		return rejected, now, nil
	case err != nil:
		return unknown, "", errors.Join(pushErr, err)
	case now == sha:
		return pushed, now, nil
	default:
		return rejected, now, nil
	}
}

// claimCommit makes the attempt's commit on base without touching any checkout.
func (a *App) claimCommit(n int, base, branch, attempt string) (string, error) {
	host, _ := os.Hostname()
	msg := fmt.Sprintf("chore: claim #%d\n\nBoard-Attempt: %s\nBoard-Branch: %s\nBoard-Host: %s\n", n, attempt, branch, host)
	return a.Git.Run("commit-tree", base+"^{tree}", "-p", base, "-m", msg)
}

// Release deletes an issue's lock, only while it still holds sha.
func (a *App) Release(n int, sha string) error {
	res, now, err := a.pushRef(lockRef(n), "", sha)
	switch {
	case err != nil:
		return exitf(ExitIncomplete, "release %s: outcome unknown: %v", lockRef(n), err)
	case res == pushed:
		a.printf("released %s\n", lockRef(n))
		return nil
	case now == "":
		return fmt.Errorf("%s does not exist", lockRef(n))
	default:
		return exitf(ExitHeld, "%s holds %s, not %s; nothing released", lockRef(n), now, sha)
	}
}
