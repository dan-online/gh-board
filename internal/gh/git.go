package gh

import "time"

// Commit is a git commit read through the REST API, so callers need not fetch it.
type Commit struct {
	SHA       string `json:"sha"`
	Message   string `json:"message"`
	Committer struct {
		Date time.Time `json:"date"`
	} `json:"committer"`
}

func (c *Client) Commit(sha string) (*Commit, error) {
	var out Commit
	err := c.get("repos/"+c.cfg.Repo+"/git/commits/"+sha, &out)
	return &out, err
}
