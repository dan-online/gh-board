// Package git runs the user's git with their own credentials.
package git

import (
	"bytes"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type Git struct{ Dir string }

// Run returns trimmed stdout. A failure carries git's stderr.
func (g Git) Run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RepoFromURL returns OWNER/NAME for a github.com remote URL in any of the
// forms git accepts: git@github.com:o/r.git, ssh://git@github.com/o/r, https://github.com/o/r.
func RepoFromURL(raw string) (string, bool) {
	if !strings.Contains(raw, "://") {
		if host, path, ok := strings.Cut(raw, ":"); ok && !strings.Contains(host, "/") {
			raw = "ssh://" + host + "/" + path
		}
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", false
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if strings.Count(path, "/") != 1 {
		return "", false
	}
	return path, true
}
