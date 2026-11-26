package board

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dan-online/gh-board/internal/forms"
)

// Ready checks the issue's form and fields, then sets the ready status.
func (a *App) Ready(number int) error {
	issue, item, err := a.item(number)
	if err != nil {
		return err
	}
	var problems []string
	if form := a.Cfg.FormFor(issue.LabelNames()); form != nil {
		data, err := os.ReadFile(a.Cfg.FormPath(form))
		if err != nil {
			return fmt.Errorf("issue form %s: %w", form.File, err)
		}
		required, err := forms.RequiredLabels(data)
		if err != nil {
			return fmt.Errorf("%s: %w", form.File, err)
		}
		problems = forms.Missing(issue.Body, required)
		for _, name := range a.Cfg.FieldNames() {
			if allowed, ok := form.Require[name]; ok && !slices.Contains(allowed, item.Values[name]) {
				problems = append(problems, fmt.Sprintf("%s must be %s on the project", name, orList(allowed)))
			}
		}
	} else {
		for _, name := range a.Cfg.FieldNames() {
			if a.Cfg.Fields[name].Required && item.Values[name] == "" {
				problems = append(problems, name+" is not set on the project")
			}
		}
	}
	if len(problems) > 0 {
		return errors.New("Missing or invalid required fields:\n" + strings.Join(problems, "\n"))
	}
	return a.GH.SetFields(item.ID, map[string]string{"Status": a.Cfg.Statuses.Ready})
}

// Status sets the item's Status to any option of the project's Status field.
func (a *App) Status(number int, status string) error {
	_, item, err := a.item(number)
	if err != nil {
		return err
	}
	return a.GH.SetFields(item.ID, map[string]string{"Status": status})
}

func orList(v []string) string {
	if len(v) < 2 {
		return strings.Join(v, "")
	}
	return strings.Join(v[:len(v)-1], ", ") + " or " + v[len(v)-1]
}
