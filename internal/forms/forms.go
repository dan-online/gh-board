// Package forms checks an issue body against the required fields of a GitHub issue form.
package forms

import (
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v4"
)

type form struct {
	Body []struct {
		Attributes struct {
			Label string `yaml:"label"`
		} `yaml:"attributes"`
		Validations struct {
			Required bool `yaml:"required"`
		} `yaml:"validations"`
	} `yaml:"body"`
}

// RequiredLabels returns the labels of the form's required fields, in form order.
func RequiredLabels(data []byte) ([]string, error) {
	var f form
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse issue form: %w", err)
	}
	var labels []string
	for _, b := range f.Body {
		if b.Validations.Required {
			labels = append(labels, b.Attributes.Label)
		}
	}
	return labels, nil
}

// Rendered forms put each answer under "### <label>". These count as no answer:
// GitHub's placeholder, list markers and checkboxes, checked or not.
var (
	noResponse = "_No response_"
	listMarker = regexp.MustCompile(`(?m)^\s*(?:- \[[ xX]\]|[-*]|[0-9]+\.)\s*`)
	whitespace = regexp.MustCompile(`\s`)
)

// Missing returns the required labels whose section in body is absent or has no answer.
func Missing(body string, required []string) []string {
	sections := map[string]string{}
	current, inSection := "", false
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if heading, ok := strings.CutPrefix(line, "### "); ok {
			current, inSection = strings.TrimRight(heading, " "), true
			continue
		}
		if inSection {
			sections[current] += "\n" + line
		}
	}
	var missing []string
	for _, label := range required {
		text := strings.ReplaceAll(sections[label], noResponse, "")
		text = listMarker.ReplaceAllString(text, "")
		if whitespace.ReplaceAllString(text, "") == "" {
			missing = append(missing, label)
		}
	}
	return missing
}
