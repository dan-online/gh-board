package gh

import (
	"fmt"
	"net/url"
	"strconv"
)

// Stack is a native GitHub pull request stack, bottom first.
type Stack struct {
	Number       int  `json:"number"`
	Open         bool `json:"open"`
	PullRequests []PR `json:"pull_requests"`
}

// Below returns the PR directly below pr in the stack, or 0.
func (s *Stack) Below(pr int) int {
	for i, p := range s.PullRequests {
		if p.Number == pr && i > 0 {
			return s.PullRequests[i-1].Number
		}
	}
	return 0
}

// Top is the stack's highest PR.
func (s *Stack) Top() int {
	if len(s.PullRequests) == 0 {
		return 0
	}
	return s.PullRequests[len(s.PullRequests)-1].Number
}

// StackFor returns the stack holding pr, or nil when it is in none.
func (c *Client) StackFor(pr int) (*Stack, error) {
	var stacks []Stack
	if err := c.get(query("repos/"+c.cfg.Repo+"/stacks", url.Values{"pull_request": {strconv.Itoa(pr)}}), &stacks); err != nil {
		return nil, err
	}
	if len(stacks) == 0 {
		return nil, nil
	}
	return &stacks[0], nil
}

func (c *Client) CreateStack(prs ...int) (*Stack, error) {
	var s Stack
	err := c.send("POST", "repos/"+c.cfg.Repo+"/stacks", map[string]any{"pull_requests": prs}, &s)
	return &s, err
}

func (c *Client) AddToStack(stack int, prs ...int) (*Stack, error) {
	var s Stack
	err := c.send("POST", fmt.Sprintf("repos/%s/stacks/%d/add", c.cfg.Repo, stack), map[string]any{"pull_requests": prs}, &s)
	return &s, err
}

func (c *Client) PR(number int) (*PR, error) {
	var pr PR
	err := c.get(fmt.Sprintf("repos/%s/pulls/%d", c.cfg.Repo, number), &pr)
	return &pr, err
}

// HasOpenPRsOnBase reports whether any open PR targets branch.
func (c *Client) HasOpenPRsOnBase(branch string) (bool, error) {
	var prs []PR
	err := c.get(query("repos/"+c.cfg.Repo+"/pulls", url.Values{"base": {branch}, "state": {"open"}, "per_page": {"1"}}), &prs)
	return len(prs) != 0, err
}

// AsyncMerge is the state of a merge submitted through the async merge API.
type AsyncMerge struct {
	Status  string `json:"status"` // pending, merged, enqueued or failed
	Details struct {
		Message string `json:"message"`
		UUID    string `json:"uuid"`
		SHA     string `json:"sha"`
	} `json:"details"`
}

// MergeAsync submits a merge that GitHub runs only while the head is still headSHA.
func (c *Client) MergeAsync(pr int, method, headSHA string) (*AsyncMerge, error) {
	var m AsyncMerge
	err := c.send("PUT", fmt.Sprintf("repos/%s/pulls/%d/merge-async", c.cfg.Repo, pr),
		map[string]any{"merge_method": method, "expected_head_sha": headSHA}, &m)
	return &m, err
}

func (c *Client) MergeAsyncResult(pr int, uuid string) (*AsyncMerge, error) {
	var m AsyncMerge
	err := c.get(fmt.Sprintf("repos/%s/pulls/%d/merge-async/%s", c.cfg.Repo, pr, url.PathEscape(uuid)), &m)
	return &m, err
}

// BlockedBy lists the issues blocking issue n.
func (c *Client) BlockedBy(n int) ([]Issue, error) {
	return getAll[Issue](c, fmt.Sprintf("repos/%s/issues/%d/dependencies/blocked_by?per_page=100", c.cfg.Repo, n))
}
