package gh

import (
	"fmt"
	"strings"
)

// Ref names an issue that a relation points at, which may be in another repo.
type Ref struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"` // open or closed, as in REST
	URL    string `json:"url"`
}

// Relations are an issue's parent, sub-issues, blockers and the issues it blocks.
type Relations struct {
	Parent    *Ref
	SubIssues []Ref
	BlockedBy []Ref
	Blocking  []Ref
}

type rawRef struct {
	Ref
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

func (r rawRef) ref() Ref {
	r.Repo = r.Repository.NameWithOwner
	r.State = strings.ToLower(r.State)
	return r.Ref
}

type rawRefs struct {
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []rawRef `json:"nodes"`
}

const refFragment = `fragment ref on Issue { number title state url repository { nameWithOwner } }`

const relationsPage = 100

var relationsQuery = fmt.Sprintf(`query($ids: [ID!]!) { nodes(ids: $ids) { ... on Issue { id number
	parent { ...ref }
	subIssues(first: %[1]d) { %[2]s }
	blockedBy(first: %[1]d) { %[2]s }
	blocking(first: %[1]d) { %[2]s }
} } }
%[3]s`, relationsPage, pageFields, refFragment)

const pageFields = "pageInfo { hasNextPage endCursor } nodes { ...ref }"

// restQuery reads the pages after the first of one relation connection.
const restQuery = `query($id: ID!, $after: String!) { node(id: $id) { ... on Issue { %s(first: %d, after: $after) { %s } } } }
%s`

// Relations reads the relations of issues by node ID, keyed by node ID, in one
// GraphQL query per 100 issues, plus one per further page of a long relation list.
// An issue GitHub does not return is an error, not an issue without relations.
func (c *Client) Relations(nodeIDs []string) (map[string]Relations, error) {
	out := map[string]Relations{}
	for start := 0; start < len(nodeIDs); start += relationsPage {
		ids := nodeIDs[start:min(start+relationsPage, len(nodeIDs))]
		var resp struct {
			Nodes []*struct {
				ID        string  `json:"id"`
				Number    int     `json:"number"`
				Parent    *rawRef `json:"parent"`
				SubIssues rawRefs `json:"subIssues"`
				BlockedBy rawRefs `json:"blockedBy"`
				Blocking  rawRefs `json:"blocking"`
			} `json:"nodes"`
		}
		if err := c.gql.Do(relationsQuery, map[string]any{"ids": ids}, &resp); err != nil {
			return nil, fmt.Errorf("read issue relations: %w", err)
		}
		for i, n := range resp.Nodes {
			if n == nil {
				return nil, fmt.Errorf("read issue relations: GitHub returned nothing for issue node %s", ids[i])
			}
			var r Relations
			if n.Parent != nil {
				p := n.Parent.ref()
				r.Parent = &p
			}
			for _, l := range []struct {
				field string
				raw   rawRefs
				dst   *[]Ref
			}{{"subIssues", n.SubIssues, &r.SubIssues}, {"blockedBy", n.BlockedBy, &r.BlockedBy}, {"blocking", n.Blocking, &r.Blocking}} {
				refs, err := c.allRefs(n.ID, l.field, l.raw)
				if err != nil {
					return nil, fmt.Errorf("read #%d %s: %w", n.Number, l.field, err)
				}
				*l.dst = refs
			}
			out[n.ID] = r
		}
	}
	return out, nil
}

// allRefs returns a connection's refs, reading the pages after first.
func (c *Client) allRefs(id, field string, first rawRefs) ([]Ref, error) {
	var refs []Ref
	q := fmt.Sprintf(restQuery, field, relationsPage, pageFields, refFragment)
	for page := first; ; {
		for _, x := range page.Nodes {
			refs = append(refs, x.ref())
		}
		if !page.PageInfo.HasNextPage {
			return refs, nil
		}
		var resp struct {
			Node map[string]rawRefs `json:"node"`
		}
		if err := c.gql.Do(q, map[string]any{"id": id, "after": page.PageInfo.EndCursor}, &resp); err != nil {
			return nil, err
		}
		next, ok := resp.Node[field]
		if !ok {
			return nil, fmt.Errorf("GitHub returned no %s page after %s", field, page.PageInfo.EndCursor)
		}
		page = next
	}
}
