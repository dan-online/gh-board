package board

import (
	"testing"

	"github.com/dan-online/gh-board/internal/gh"
)

func TestAttemptPRFindsTheClaimPRAfterItsBodyWasRewritten(t *testing.T) {
	pr := func(n int, state, body string) gh.PR { return gh.PR{Number: n, State: state, Body: body} }
	marked := "Closes #7\n\n" + attemptMarker("abc123")
	for _, tc := range []struct {
		name string
		prs  []gh.PR
		want int // 0 means none
	}{
		{"body rewritten without the marker", []gh.PR{pr(5, "closed", "old attempt"), pr(9, "open", "Closes #7\n\nNew text")}, 9},
		{"marker wins over an open unmarked PR", []gh.PR{pr(8, "open", "other"), pr(9, "closed", marked)}, 9},
		{"closed unmarked PR is another attempt's", []gh.PR{pr(5, "closed", "Closes #7")}, 0},
		{"two open unmarked PRs are ambiguous", []gh.PR{pr(8, "open", ""), pr(9, "open", "")}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := 0
			if p := attemptPR(tc.prs, "abc123"); p != nil {
				got = p.Number
			}
			if got != tc.want {
				t.Fatalf("got PR #%d, want #%d", got, tc.want)
			}
		})
	}
}
