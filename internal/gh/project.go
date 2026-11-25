package gh

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type Field struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	DataType string   `json:"data_type"`
	Options  []Option `json:"options"`
}

type Option struct {
	ID   string `json:"id"`
	Name struct {
		Raw string `json:"raw"`
	} `json:"name"`
}

// OptionNames lists a single-select field's options in board order.
func (f *Field) OptionNames() []string {
	names := make([]string, len(f.Options))
	for i, o := range f.Options {
		names[i] = o.Name.Raw
	}
	return names
}

func (c *Client) project() (string, error) {
	if c.projectPath != "" {
		return c.projectPath, nil
	}
	var owner struct {
		Type string `json:"type"`
	}
	if err := c.get("users/"+url.PathEscape(c.cfg.Project.Owner), &owner); err != nil {
		return "", fmt.Errorf("project owner %s: %w", c.cfg.Project.Owner, err)
	}
	c.ownerIsOrg = owner.Type == "Organization"
	kind := "users"
	if c.ownerIsOrg {
		kind = "orgs"
	}
	c.projectPath = fmt.Sprintf("%s/%s/projectsV2/%d", kind, url.PathEscape(c.cfg.Project.Owner), c.cfg.Project.Number)
	return c.projectPath, nil
}

func (c *Client) Fields() ([]Field, error) {
	if c.fields != nil {
		return c.fields, nil
	}
	p, err := c.project()
	if err != nil {
		return nil, err
	}
	fields, err := getAll[Field](c, p+"/fields?per_page=100")
	if err != nil {
		return nil, fmt.Errorf("read project fields: %w", err)
	}
	c.fields = fields
	return fields, nil
}

func (c *Client) Field(name string) (*Field, error) {
	fields, err := c.Fields()
	if err != nil {
		return nil, err
	}
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i], nil
		}
	}
	return nil, fmt.Errorf("project field %s not found", name)
}

// Item is an issue's card on the project.
type Item struct {
	ID       int64
	Archived bool
	Issue    Issue
	Values   map[string]string // single-select field name to option name
	OpenPRs  []int             // open linked pull requests
}

type rawItem struct {
	ID          int64   `json:"id"`
	ArchivedAt  *string `json:"archived_at"`
	Content     Issue   `json:"content"`
	ContentType string  `json:"content_type"`
	Fields      []struct {
		Name     string `json:"name"`
		DataType string `json:"data_type"`
		Value    any    `json:"value"`
	} `json:"fields"`
}

func (r *rawItem) item() Item {
	it := Item{ID: r.ID, Archived: r.ArchivedAt != nil, Issue: r.Content, Values: map[string]string{}}
	for _, f := range r.Fields {
		switch f.DataType {
		case "single_select":
			if v, ok := f.Value.(map[string]any); ok {
				if name, ok := v["name"].(map[string]any); ok {
					it.Values[f.Name], _ = name["raw"].(string)
				}
			}
		case "linked_pull_requests":
			prs, _ := f.Value.([]any)
			for _, p := range prs {
				pr, _ := p.(map[string]any)
				if pr["state"] == "open" {
					if n, ok := pr["number"].(float64); ok {
						it.OpenPRs = append(it.OpenPRs, int(n))
					}
				}
			}
		}
	}
	slices.Sort(it.OpenPRs)
	return it
}

// fieldIDs returns the IDs of the Status field, the configured fields and
// the linked pull requests field, for the items endpoint's fields parameter.
func (c *Client) fieldIDs() (string, error) {
	fields, err := c.Fields()
	if err != nil {
		return "", err
	}
	var ids []string
	for _, f := range fields {
		_, configured := c.cfg.Fields[f.Name]
		if f.Name == "Status" || configured || f.DataType == "linked_pull_requests" {
			ids = append(ids, strconv.FormatInt(f.ID, 10))
		}
	}
	return strings.Join(ids, ","), nil
}

func (c *Client) items(q string) ([]Item, error) {
	p, err := c.project()
	if err != nil {
		return nil, err
	}
	ids, err := c.fieldIDs()
	if err != nil {
		return nil, err
	}
	v := url.Values{"fields": {ids}, "per_page": {"100"}}
	if q != "" {
		v.Set("q", q)
	}
	raw, err := getAll[rawItem](c, query(p+"/items", v))
	if err != nil {
		return nil, fmt.Errorf("read project items: %w", err)
	}
	var items []Item
	for i := range raw {
		if raw[i].ContentType == "Issue" {
			items = append(items, raw[i].item())
		}
	}
	return items, nil
}

// FindItem returns the issue's project item. The project search narrows
// the list; the URL match picks the item.
func (c *Client) FindItem(issue *Issue) (*Item, error) {
	items, err := c.items(fmt.Sprintf("repo:%s %d", c.cfg.Repo, issue.Number))
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Issue.HTMLURL == issue.HTMLURL {
			return &items[i], nil
		}
	}
	return nil, fmt.Errorf("issue #%d is not on project %s #%d", issue.Number, c.cfg.Project.Owner, c.cfg.Project.Number)
}

// Items returns the project's items for issues in the board's repo, in board order.
// A project can hold other repos' issues, whose numbers mean nothing here.
func (c *Client) Items() ([]Item, error) {
	items, err := c.items("")
	if err != nil {
		return nil, err
	}
	return inRepo(items, c.cfg.Repo), nil
}

func inRepo(items []Item, repo string) []Item {
	prefix := strings.ToLower("https://github.com/" + repo + "/issues/")
	return slices.DeleteFunc(items, func(it Item) bool { return !strings.HasPrefix(strings.ToLower(it.Issue.HTMLURL), prefix) })
}

func (c *Client) AddItem(issue *Issue) (int64, error) {
	p, err := c.project()
	if err != nil {
		return 0, err
	}
	var out struct {
		ID int64 `json:"id"`
	}
	err = c.send("POST", p+"/items", map[string]any{"type": "Issue", "id": issue.ID}, &out)
	return out.ID, err
}

// SetFields sets single-select fields on an item in one request.
// Values maps field names to option names.
func (c *Client) SetFields(itemID int64, values map[string]string) error {
	var updates []map[string]any
	for _, name := range slices.Sorted(maps.Keys(values)) {
		f, err := c.Field(name)
		if err != nil {
			return err
		}
		idx := slices.IndexFunc(f.Options, func(o Option) bool { return o.Name.Raw == values[name] })
		if idx < 0 {
			return fmt.Errorf("project field %s has no option %q; options: %s", name, values[name], strings.Join(f.OptionNames(), ", "))
		}
		updates = append(updates, map[string]any{"id": f.ID, "value": f.Options[idx].ID})
	}
	p, err := c.project()
	if err != nil {
		return err
	}
	return c.send("PATCH", fmt.Sprintf("%s/items/%d", p, itemID), map[string]any{"fields": updates}, nil)
}

// ViewerCanUpdateProject asks GraphQL whether the token can edit the project.
func (c *Client) ViewerCanUpdateProject() (bool, error) {
	if _, err := c.project(); err != nil {
		return false, err
	}
	kind := "user"
	if c.ownerIsOrg {
		kind = "organization"
	}
	q := fmt.Sprintf(`query($login: String!, $number: Int!) { owner: %s(login: $login) { projectV2(number: $number) { viewerCanUpdate } } }`, kind)
	var out struct {
		Owner struct {
			ProjectV2 struct {
				ViewerCanUpdate bool `json:"viewerCanUpdate"`
			} `json:"projectV2"`
		} `json:"owner"`
	}
	err := c.gql.Do(q, map[string]any{"login": c.cfg.Project.Owner, "number": c.cfg.Project.Number}, &out)
	return out.Owner.ProjectV2.ViewerCanUpdate, err
}
