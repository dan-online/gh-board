// Package gh is the GitHub side of the board: issues, project items and pull requests.
package gh

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/dan-online/gh-board/internal/config"
	"github.com/gofri/go-github-ratelimit/v2/github_ratelimit"
	"github.com/gofri/go-github-ratelimit/v2/github_ratelimit/github_secondary_ratelimit"
)

type Client struct {
	rest *api.RESTClient
	gql  *api.GraphQLClient
	cfg  *config.Config

	projectPath string
	ownerIsOrg  bool
	fields      []Field
}

func New(cfg *config.Config) (*Client, error) {
	transport := transport()
	rest, err := api.NewRESTClient(api.ClientOptions{
		Headers:   map[string]string{"X-GitHub-Api-Version": cfg.APIVersion},
		Timeout:   30 * time.Second,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}
	gql, err := api.NewGraphQLClient(api.ClientOptions{Timeout: 30 * time.Second, Transport: transport})
	if err != nil {
		return nil, err
	}
	return &Client{rest: rest, gql: gql, cfg: cfg}, nil
}

// transport waits out secondary rate limits and resends the request after them,
// for at most three minutes in total.
func transport() http.RoundTripper {
	return github_ratelimit.New(http.DefaultTransport,
		github_secondary_ratelimit.WithTotalSleepLimit(3*time.Minute, nil))
}

// Definite reports whether err means the write did not happen: GitHub rejected
// it, or it failed before any request was sent. A timeout or a 5xx is not definite.
func Definite(err error) bool {
	var httpErr *api.HTTPError
	var local *LocalError
	return errors.As(err, &local) || errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500
}

// LocalError is a failure before any request was sent.
type LocalError struct{ Err error }

func (e *LocalError) Error() string { return e.Err.Error() }
func (e *LocalError) Unwrap() error { return e.Err }

func localf(format string, a ...any) error { return &LocalError{Err: fmt.Errorf(format, a...)} }

// NotFound reports whether err is a 404.
func NotFound(err error) bool {
	var httpErr *api.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

func (c *Client) get(path string, out any) error { return c.rest.Get(path, out) }

func (c *Client) send(method, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.rest.Do(method, path, bytes.NewReader(body), out)
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// getAll follows Link headers and decodes every page of a JSON array into out.
func getAll[T any](c *Client, path string) ([]T, error) {
	var all []T
	for path != "" {
		resp, err := c.rest.Request(http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var page []T
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		path = ""
		if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
			path = m[1]
		}
	}
	return all, nil
}

func query(path string, v url.Values) string { return path + "?" + v.Encode() }

// Repo is the subset of the repository the board needs.
type Repo struct {
	DefaultBranch string `json:"default_branch"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

func (c *Client) Repo() (*Repo, error) {
	var r Repo
	if err := c.get("repos/"+c.cfg.Repo, &r); err != nil {
		return nil, fmt.Errorf("read %s: %w", c.cfg.Repo, err)
	}
	return &r, nil
}

// Viewer returns the login and the OAuth scopes of the current token.
// Scopes is nil for tokens that do not report them, such as fine-grained tokens.
func (c *Client) Viewer() (login string, scopes []string, err error) {
	resp, err := c.rest.Request(http.MethodGet, "user", nil)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var u struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", nil, err
	}
	if h, ok := resp.Header["X-Oauth-Scopes"]; ok {
		scopes = []string{}
		for _, s := range strings.Split(strings.Join(h, ","), ",") {
			if s = strings.TrimSpace(s); s != "" {
				scopes = append(scopes, s)
			}
		}
	}
	return u.Login, scopes, nil
}
