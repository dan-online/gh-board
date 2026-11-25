// Package config loads and checks .github/board.yml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
	"golang.org/x/mod/semver"
)

// Path is where a repository keeps its board config, relative to its root.
const Path = ".github/board.yml"

type Config struct {
	Repo       string           `yaml:"repo"`
	Remote     string           `yaml:"remote"`
	Project    Project          `yaml:"project"`
	APIVersion string           `yaml:"api_version"`
	MinVersion string           `yaml:"min_version"`
	Worktrees  string           `yaml:"worktrees"`
	Types      []string         `yaml:"types"`
	Statuses   Statuses         `yaml:"statuses"`
	Fields     map[string]Field `yaml:"fields"`
	Queue      *Queue           `yaml:"queue"`
	Forms      []Form           `yaml:"forms"`

	// Root is the checkout the config was read from.
	Root string `yaml:"-"`
}

type Project struct {
	Owner  string `yaml:"owner"`
	Number int    `yaml:"number"`
}

type Statuses struct {
	New     string `yaml:"new"`
	Ready   string `yaml:"ready"`
	Claimed string `yaml:"claimed"`
}

type Field struct {
	Options  []string `yaml:"options"`
	Required bool     `yaml:"required"`
	Default  string   `yaml:"default"`
}

type Queue struct {
	Sort string `yaml:"sort"`
}

type Form struct {
	Label   string              `yaml:"label"`
	Default bool                `yaml:"default"`
	File    string              `yaml:"file"`
	Require map[string][]string `yaml:"require"`
}

// Owner and Name split Repo.
func (c *Config) Owner() string { o, _, _ := strings.Cut(c.Repo, "/"); return o }
func (c *Config) Name() string  { _, n, _ := strings.Cut(c.Repo, "/"); return n }

// Load reads the config from the checkout at root.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, Path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no %s in %s", Path, root)
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", Path, err)
	}
	c.Root = root
	return c, nil
}

// Parse decodes and validates a config. Unknown keys are errors.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	var errs []error
	need := func(key, v string) {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
		}
	}
	need("repo", c.Repo)
	if c.Repo != "" && (c.Owner() == "" || c.Name() == "" || strings.Count(c.Repo, "/") != 1) {
		errs = append(errs, fmt.Errorf("repo must be OWNER/NAME, got %q", c.Repo))
	}
	need("remote", c.Remote)
	need("project.owner", c.Project.Owner)
	if c.Project.Number <= 0 {
		errs = append(errs, errors.New("project.number must be a positive number"))
	}
	need("api_version", c.APIVersion)
	need("worktrees", c.Worktrees)
	if c.MinVersion != "" && !semver.IsValid(c.MinVersion) {
		errs = append(errs, fmt.Errorf("min_version %q is not a version like v1.2.3", c.MinVersion))
	}
	if len(c.Types) == 0 {
		errs = append(errs, errors.New("types needs at least one issue type"))
	}
	need("statuses.new", c.Statuses.New)
	need("statuses.ready", c.Statuses.Ready)
	need("statuses.claimed", c.Statuses.Claimed)
	for name, f := range c.Fields {
		if name == "Status" {
			errs = append(errs, errors.New("fields.Status: set Status through statuses, not fields"))
		}
		if len(f.Options) == 0 {
			errs = append(errs, fmt.Errorf("fields.%s.options needs at least one option", name))
		}
		if f.Default != "" && !slices.Contains(f.Options, f.Default) {
			errs = append(errs, fmt.Errorf("fields.%s.default %q is not one of its options", name, f.Default))
		}
	}
	if c.Queue != nil {
		if _, ok := c.Fields[c.Queue.Sort]; !ok {
			errs = append(errs, fmt.Errorf("queue.sort %q is not a field in fields", c.Queue.Sort))
		}
	}
	defaults := 0
	for i, f := range c.Forms {
		key := fmt.Sprintf("forms[%d]", i)
		if f.Default {
			defaults++
		}
		if f.Default == (f.Label != "") {
			errs = append(errs, fmt.Errorf("%s needs exactly one of label or default", key))
		}
		if f.File == "" {
			errs = append(errs, fmt.Errorf("%s.file is required", key))
		}
		for name, values := range f.Require {
			field, ok := c.Fields[name]
			if !ok {
				errs = append(errs, fmt.Errorf("%s.require.%s is not a field in fields", key, name))
				continue
			}
			for _, v := range values {
				if !slices.Contains(field.Options, v) {
					errs = append(errs, fmt.Errorf("%s.require.%s: %q is not one of its options", key, name, v))
				}
			}
		}
	}
	if defaults > 1 {
		errs = append(errs, errors.New("forms has more than one default"))
	}
	return errors.Join(errs...)
}

// FormFor returns the form for an issue with these labels, or nil when no form applies.
func (c *Config) FormFor(labels []string) *Form {
	var fallback *Form
	for i := range c.Forms {
		f := &c.Forms[i]
		if f.Label != "" && slices.Contains(labels, f.Label) {
			return f
		}
		if f.Default {
			fallback = f
		}
	}
	return fallback
}

// FormPath is where a form file lives in the checkout.
func (c *Config) FormPath(f *Form) string {
	return filepath.Join(c.Root, ".github", "ISSUE_TEMPLATE", f.File)
}

// CheckVersion fails when this binary is older than min_version.
// A build without a release version (a local build) always passes.
func (c *Config) CheckVersion(version string) error {
	if c.MinVersion == "" || !semver.IsValid(version) {
		return nil
	}
	if semver.Compare(version, c.MinVersion) < 0 {
		return fmt.Errorf("gh-board %s is older than min_version %s in %s; run: gh extension remove board && gh extension install dan-online/gh-board --pin %s", version, c.MinVersion, Path, c.MinVersion)
	}
	return nil
}

// FieldNames returns the configured field names in a stable order.
func (c *Config) FieldNames() []string {
	return slices.Sorted(maps.Keys(c.Fields))
}
