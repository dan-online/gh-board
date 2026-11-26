package gh

import (
	"fmt"
	"slices"
	"strings"
)

type CommitResult struct {
	Commit *Commit
	Err    error
}

// Commits reads commits in batches, falling back to REST for each missing object.
// Individual REST failures are returned alongside the other commits.
func (c *Client) Commits(shas []string) (map[string]CommitResult, error) {
	results := make(map[string]CommitResult, len(shas))
	for batch := range slices.Chunk(shas, 50) {
		var q strings.Builder
		q.WriteString(`query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) {`)
		for i, sha := range batch {
			fmt.Fprintf(&q, ` c%d: object(oid: %q) { ... on Commit { oid message committer { date } } }`, i, sha)
		}
		q.WriteString(" } }")
		var out struct {
			Repository map[string]*struct {
				OID string `json:"oid"`
				Commit
			} `json:"repository"`
		}
		if err := c.gql.Do(q.String(), map[string]any{"owner": c.cfg.Owner(), "name": c.cfg.Name()}, &out); err != nil {
			return nil, fmt.Errorf("read lock commits: %w", err)
		}
		for i, sha := range batch {
			if hit := out.Repository[fmt.Sprintf("c%d", i)]; hit != nil && hit.OID != "" {
				hit.SHA = hit.OID
				results[sha] = CommitResult{Commit: &hit.Commit}
			} else {
				commit, err := c.Commit(sha)
				results[sha] = CommitResult{Commit: commit, Err: err}
			}
		}
	}
	return results, nil
}
