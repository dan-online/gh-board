package gh

import (
	"fmt"
	"net/url"
)

type PR struct {
	Number   int     `json:"number"`
	HTMLURL  string  `json:"html_url"`
	State    string  `json:"state"`
	Body     string  `json:"body"`
	Merged   bool    `json:"merged"`
	MergedAt *string `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (c *Client) CreatePR(title, head, base, body string) (*PR, error) {
	var pr PR
	err := c.send("POST", "repos/"+c.cfg.Repo+"/pulls", map[string]any{
		"title": title, "head": head, "base": base, "body": body, "draft": true,
	}, &pr)
	return &pr, err
}

func (c *Client) ClosePR(number int) error {
	return c.send("PATCH", fmt.Sprintf("repos/%s/pulls/%d", c.cfg.Repo, number), map[string]any{"state": "closed"}, nil)
}

// PRsForBranch lists pull requests of any state whose head is branch in the board's repo.
func (c *Client) PRsForBranch(branch string) ([]PR, error) {
	v := url.Values{"head": {c.cfg.Owner() + ":" + branch}, "state": {"all"}, "per_page": {"100"}}
	return getAll[PR](c, query("repos/"+c.cfg.Repo+"/pulls", v))
}
