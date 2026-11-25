package board

import (
	"testing"

	"github.com/dan-online/gh-board/internal/gh"
)

func TestMergeRefusesAPRAboveAnOpenOne(t *testing.T) {
	st := &gh.Stack{PullRequests: []gh.PR{{Number: 10, State: "closed"}, {Number: 11, State: "open"}, {Number: 12, State: "open"}}}
	if got := openBelow(st, 12); got != 11 {
		t.Fatalf("openBelow(12) = %d, want 11", got)
	}
	if got := openBelow(st, 11); got != 0 {
		t.Fatalf("openBelow(11) = %d, want 0: only a merged PR is below", got)
	}
	if got := openBelow(nil, 12); got != 0 {
		t.Fatalf("unstacked PR: got %d", got)
	}
}
