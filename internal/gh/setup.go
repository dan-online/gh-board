package gh

import (
	"fmt"
)

// APIVersion is the REST API version new configs pin.
const APIVersion = "2026-03-10"

type ProjectRef struct {
	Owner  string
	Number int
	Title  string
}

// RepoInfo is what init needs to know about the repository.
type RepoInfo struct {
	ID         string
	OwnerID    string
	OwnerLogin string
	OwnerIsOrg bool
	Projects   []ProjectRef // open projects linked to the repo
	IssueTypes []string     // enabled issue types
}

func (c *Client) RepoInfo() (*RepoInfo, error) {
	var out struct {
		Repository struct {
			ID    string `json:"id"`
			Owner struct {
				Typename string `json:"__typename"`
				ID       string `json:"id"`
				Login    string `json:"login"`
			} `json:"owner"`
			ProjectsV2 struct {
				Nodes []struct {
					Number int    `json:"number"`
					Title  string `json:"title"`
					Closed bool   `json:"closed"`
					Owner  struct {
						Login string `json:"login"`
					} `json:"owner"`
				} `json:"nodes"`
			} `json:"projectsV2"`
			IssueTypes struct {
				Nodes []struct {
					Name      string `json:"name"`
					IsEnabled bool   `json:"isEnabled"`
				} `json:"nodes"`
			} `json:"issueTypes"`
		} `json:"repository"`
	}
	q := `query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) {
		id
		owner { __typename id login }
		projectsV2(first: 50) { nodes { number title closed owner { ... on Organization { login } ... on User { login } } } }
		issueTypes(first: 50) { nodes { name isEnabled } }
	} }`
	if err := c.gql.Do(q, map[string]any{"owner": c.cfg.Owner(), "name": c.cfg.Name()}, &out); err != nil {
		return nil, fmt.Errorf("read %s: %w", c.cfg.Repo, err)
	}
	r := out.Repository
	info := &RepoInfo{ID: r.ID, OwnerID: r.Owner.ID, OwnerLogin: r.Owner.Login, OwnerIsOrg: r.Owner.Typename == "Organization"}
	for _, p := range r.ProjectsV2.Nodes {
		if !p.Closed {
			info.Projects = append(info.Projects, ProjectRef{Owner: p.Owner.Login, Number: p.Number, Title: p.Title})
		}
	}
	for _, t := range r.IssueTypes.Nodes {
		if t.IsEnabled {
			info.IssueTypes = append(info.IssueTypes, t.Name)
		}
	}
	return info, nil
}

// CreateProject creates a project owned by ownerID and linked to repoID.
func (c *Client) CreateProject(ownerID, repoID, title string) (int, error) {
	var out struct {
		CreateProjectV2 struct {
			ProjectV2 struct {
				Number int `json:"number"`
			} `json:"projectV2"`
		} `json:"createProjectV2"`
	}
	q := `mutation($owner: ID!, $repo: ID!, $title: String!) {
		createProjectV2(input: {ownerId: $owner, repositoryId: $repo, title: $title}) { projectV2 { number } } }`
	err := c.gql.Do(q, map[string]any{"owner": ownerID, "repo": repoID, "title": title}, &out)
	return out.CreateProjectV2.ProjectV2.Number, err
}

// SelectField is a project single-select field as GraphQL sees it, with node IDs.
type SelectField struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Options []SelectOption `json:"options"`
}

type SelectOption struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// SelectFields returns the project's node ID and its single-select fields.
func (c *Client) SelectFields() (string, []SelectField, error) {
	if _, err := c.project(); err != nil {
		return "", nil, err
	}
	kind := "user"
	if c.ownerIsOrg {
		kind = "organization"
	}
	q := fmt.Sprintf(`query($login: String!, $number: Int!) { owner: %s(login: $login) { projectV2(number: $number) {
		id
		fields(first: 50) { nodes { ... on ProjectV2SingleSelectField { id name options { id name color description } } } }
	} } }`, kind)
	var out struct {
		Owner struct {
			ProjectV2 struct {
				ID     string `json:"id"`
				Fields struct {
					Nodes []SelectField `json:"nodes"`
				} `json:"fields"`
			} `json:"projectV2"`
		} `json:"owner"`
	}
	if err := c.gql.Do(q, map[string]any{"login": c.cfg.Project.Owner, "number": c.cfg.Project.Number}, &out); err != nil {
		return "", nil, err
	}
	var fields []SelectField
	for _, f := range out.Owner.ProjectV2.Fields.Nodes {
		if f.ID != "" {
			fields = append(fields, f)
		}
	}
	return out.Owner.ProjectV2.ID, fields, nil
}

// SetOptions replaces a field's options. Existing options keep their values
// on items only when they are passed back with their IDs.
func (c *Client) SetOptions(fieldID string, options []SelectOption) error {
	q := `mutation($field: ID!, $options: [ProjectV2SingleSelectFieldOptionInput!]) {
		updateProjectV2Field(input: {fieldId: $field, singleSelectOptions: $options}) { clientMutationId } }`
	return c.gql.Do(q, map[string]any{"field": fieldID, "options": options}, nil)
}

func (c *Client) CreateSelectField(projectID, name string, options []string) error {
	opts := make([]SelectOption, len(options))
	for i, o := range options {
		opts[i] = SelectOption{Name: o, Color: "GRAY"}
	}
	q := `mutation($project: ID!, $name: String!, $options: [ProjectV2SingleSelectFieldOptionInput!]) {
		createProjectV2Field(input: {projectId: $project, dataType: SINGLE_SELECT, name: $name, singleSelectOptions: $options}) { clientMutationId } }`
	return c.gql.Do(q, map[string]any{"project": projectID, "name": name, "options": opts}, nil)
}
