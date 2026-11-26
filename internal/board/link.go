package board

import (
	"fmt"

	"github.com/dan-online/gh-board/internal/gh"
)

type LinkOpts struct {
	Issue     int
	Parent    string
	BlockedBy []string
}

// Link adds relations to an existing issue, checking every target first.
func (a *App) Link(o LinkOpts) error {
	if o.Parent == "" && len(o.BlockedBy) == 0 {
		return exitf(ExitUsage, "pass --parent or --blocked-by")
	}
	issue, err := a.GH.Issue(o.Issue)
	if err != nil {
		return err
	}
	parentPath, blockers, err := a.relationTargets(o.Parent, o.BlockedBy)
	if err != nil {
		return err
	}
	steps := a.relationSteps(issue, o.Parent, parentPath, o.BlockedBy, blockers)
	for i, s := range steps {
		if err := s.run(); err != nil {
			rest := remaining(steps[i+1:])
			if !gh.Definite(err) {
				return exitf(ExitIncomplete, "%s: %v\nThis link may exist: check gh board show %d before repeating it. Still missing after it: %s", s.name, err, issue.Number, rest)
			}
			return fmt.Errorf("%s: %w\nStill missing after it: %s", s.name, err, rest)
		}
		a.printf("#%d: %s\n", issue.Number, s.name)
	}
	return nil
}
