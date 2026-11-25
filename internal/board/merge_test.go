package board

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dan-online/gh-board/internal/gh"
)

type fakeMerge struct {
	stack    *gh.Stack
	stackErr error
	merges   int
}

func (f *fakeMerge) PR(n int) (*gh.PR, error) {
	pr := &gh.PR{Number: n, State: "open"}
	pr.Head.SHA, pr.Head.Ref = "abc", "12-x"
	return pr, nil
}
func (f *fakeMerge) StackFor(int) (*gh.Stack, error) { return f.stack, f.stackErr }
func (f *fakeMerge) MergeAsync(int, string, string) (*gh.AsyncMerge, error) {
	f.merges++
	m := &gh.AsyncMerge{Status: "merged"}
	return m, nil
}
func (f *fakeMerge) MergeAsyncResult(int, string) (*gh.AsyncMerge, error) {
	return nil, errors.New("unused")
}
func (f *fakeMerge) OpenPRsOnBase(string) ([]gh.PR, error) { return []gh.PR{{Number: 99}}, nil }

func TestMergeRefusesAPRAboveAnOpenOne(t *testing.T) {
	f := &fakeMerge{stack: &gh.Stack{Number: 3, PullRequests: []gh.PR{{Number: 10, State: "closed"}, {Number: 11, State: "open"}, {Number: 12, State: "open"}}}}
	a := &App{Out: &bytes.Buffer{}}
	err := a.mergePR(f, 12, "abc", "squash")
	if err == nil || !strings.Contains(err.Error(), "starting with #11") || f.merges != 0 {
		t.Fatalf("got %v after %d merges", err, f.merges)
	}
	if err := a.mergePR(f, 11, "abc", "squash"); err != nil || f.merges != 1 {
		t.Fatalf("bottom open PR above a merged one: %v, %d merges", err, f.merges)
	}
}

func TestMergeStopsWhenTheStackCannotBeRead(t *testing.T) {
	f := &fakeMerge{stackErr: errors.New("boom")}
	if err := (&App{Out: &bytes.Buffer{}}).mergePR(f, 12, "abc", "squash"); err == nil || f.merges != 0 {
		t.Fatalf("merged without knowing the stack: %v", err)
	}
}
