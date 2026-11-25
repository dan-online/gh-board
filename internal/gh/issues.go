package gh

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
)

type Issue struct {
	ID          int64     `json:"id"`
	NodeID      string    `json:"node_id"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	HTMLURL     string    `json:"html_url"`
	PullRequest *struct{} `json:"pull_request"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Dependencies struct {
		BlockedBy int `json:"blocked_by"`
	} `json:"issue_dependencies_summary"`
}

func (i *Issue) LabelNames() []string {
	names := make([]string, len(i.Labels))
	for n, l := range i.Labels {
		names[n] = l.Name
	}
	return names
}

var issueURL = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/issues/([1-9][0-9]*)$`)
var issueNumber = regexp.MustCompile(`^[1-9][0-9]*$`)

// IssuePath turns an issue number or URL into its REST path.
func (c *Client) IssuePath(ref string) (string, error) {
	if m := issueURL.FindStringSubmatch(ref); m != nil {
		return "repos/" + m[1] + "/issues/" + m[2], nil
	}
	if issueNumber.MatchString(ref) {
		return "repos/" + c.cfg.Repo + "/issues/" + ref, nil
	}
	return "", fmt.Errorf("not an issue number or URL: %s", ref)
}

// IssueAt reads the issue at a REST path and refuses pull requests.
func (c *Client) IssueAt(path string) (*Issue, error) {
	var i Issue
	if err := c.get(path, &i); err != nil {
		return nil, err
	}
	if i.PullRequest != nil {
		return nil, fmt.Errorf("%s is a pull request, not an issue", path)
	}
	return &i, nil
}

func (c *Client) Issue(number int) (*Issue, error) {
	i, err := c.IssueAt("repos/" + c.cfg.Repo + "/issues/" + strconv.Itoa(number))
	if err != nil {
		return nil, fmt.Errorf("issue #%d not found: %w", number, err)
	}
	return i, nil
}

func (c *Client) LabelExists(name string) error {
	var out struct{}
	return c.get("repos/"+c.cfg.Repo+"/labels/"+url.PathEscape(name), &out)
}

func (c *Client) CreateIssue(title, body, issueType string, labels []string) (*Issue, error) {
	if labels == nil {
		labels = []string{}
	}
	var i Issue
	err := c.send("POST", "repos/"+c.cfg.Repo+"/issues", map[string]any{
		"title": title, "body": body, "type": issueType, "labels": labels,
	}, &i)
	return &i, err
}

func (c *Client) AddSubIssue(parentPath string, child *Issue) error {
	return c.send("POST", parentPath+"/sub_issues", map[string]any{"sub_issue_id": child.ID}, nil)
}

func (c *Client) AddBlockedBy(issue *Issue, blocker *Issue) error {
	return c.send("POST", fmt.Sprintf("repos/%s/issues/%d/dependencies/blocked_by", c.cfg.Repo, issue.Number),
		map[string]any{"issue_id": blocker.ID}, nil)
}
