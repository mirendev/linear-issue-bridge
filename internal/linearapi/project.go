package linearapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Project is a Linear project as the public roadmap needs it.
//
// Linear's field naming is confusing here and worth stating once: Description
// is the short one-liner (the "summary" in Linear's own UI), while Content is
// the long project document. The authored "Roadmap Summary:" blocks live in
// Content, not Description.
type Project struct {
	ID            string
	Name          string
	Description   string
	Content       string
	Priority      int
	CompletedAt   *time.Time
	Status        ProjectStatus
	Labels        []Label
	ExternalLinks []ExternalLink
}

type ProjectStatus struct {
	Name string
	Type string // backlog, planned, started, completed, paused, canceled
}

type ExternalLink struct {
	URL   string
	Label string
}

func (p *Project) HasLabel(name string) bool {
	for _, l := range p.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

// LabelNames returns the project's label names in Linear's order.
func (p *Project) LabelNames() []string {
	names := make([]string, 0, len(p.Labels))
	for _, l := range p.Labels {
		names = append(names, l.Name)
	}
	return names
}

// roadmapProjectsQuery selects projects carrying any of the roadmap opt-in
// labels. Cursor pagination keeps each page small, which sidesteps Linear's
// query-complexity cap: the website had to hand-tune page sizes down to 50
// projects to stay under it, and still fell over the first time the nested
// label and link selections grew.
const roadmapProjectsQuery = `
query RoadmapProjects($labelNames: [String!]!, $cursor: String) {
  projects(
    filter: {
      labels: { some: { name: { in: $labelNames } } }
    }
    first: 50
    after: $cursor
    orderBy: updatedAt
  ) {
    nodes {
      id
      name
      description
      content
      priority
      completedAt
      status { name type }
      labels { nodes { id name color } }
      externalLinks { nodes { url label } }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
`

// roadmapIssuesQuery selects issues carrying one of the per-column opt-in
// labels, from anywhere in the workspace rather than a single team. Terminal
// states are excluded here and re-checked in the roadmap package.
const roadmapIssuesQuery = `
query RoadmapIssues($labelNames: [String!]!, $cursor: String) {
  issues(
    filter: {
      labels: { some: { name: { in: $labelNames } } }
      state: { type: { nin: ["completed", "canceled", "cancelled"] } }
    }
    first: 50
    after: $cursor
    orderBy: updatedAt
  ) {
    nodes {
      id
      identifier
      title
      description
      url
      priority
      createdAt
      updatedAt
      state { name color type }
      labels { nodes { id name color } }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
`

type projectJSON struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Content     string     `json:"content"`
	Priority    int        `json:"priority"`
	CompletedAt *time.Time `json:"completedAt"`
	Status      *struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"status"`
	Labels struct {
		Nodes []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"nodes"`
	} `json:"labels"`
	ExternalLinks struct {
		Nodes []struct {
			URL   string `json:"url"`
			Label string `json:"label"`
		} `json:"nodes"`
	} `json:"externalLinks"`
}

func (j projectJSON) toProject() *Project {
	p := &Project{
		ID:          j.ID,
		Name:        j.Name,
		Description: j.Description,
		Content:     j.Content,
		Priority:    j.Priority,
		CompletedAt: j.CompletedAt,
	}
	if j.Status != nil {
		p.Status = ProjectStatus{Name: j.Status.Name, Type: j.Status.Type}
	}
	for _, l := range j.Labels.Nodes {
		p.Labels = append(p.Labels, Label{ID: l.ID, Name: l.Name, Color: l.Color})
	}
	for _, l := range j.ExternalLinks.Nodes {
		p.ExternalLinks = append(p.ExternalLinks, ExternalLink{URL: l.URL, Label: l.Label})
	}
	return p
}

// FetchRoadmapProjects returns every project carrying one of labelNames.
func (c *Client) FetchRoadmapProjects(ctx context.Context, labelNames []string) ([]*Project, error) {
	var all []*Project
	var cursor *string

	for {
		vars := map[string]any{"labelNames": labelNames}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		data, err := c.do(ctx, roadmapProjectsQuery, vars)
		if err != nil {
			return nil, err
		}

		var resp struct {
			Projects struct {
				Nodes    []projectJSON `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("decode roadmap projects: %w", err)
		}

		for i := range resp.Projects.Nodes {
			all = append(all, resp.Projects.Nodes[i].toProject())
		}

		if !resp.Projects.PageInfo.HasNextPage {
			break
		}
		cursor = &resp.Projects.PageInfo.EndCursor
	}

	return all, nil
}

// FetchRoadmapIssues returns every issue carrying one of labelNames.
func (c *Client) FetchRoadmapIssues(ctx context.Context, labelNames []string) ([]*Issue, error) {
	var all []*Issue
	var cursor *string

	for {
		vars := map[string]any{"labelNames": labelNames}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		data, err := c.do(ctx, roadmapIssuesQuery, vars)
		if err != nil {
			return nil, err
		}

		var resp struct {
			Issues struct {
				Nodes    []issueJSON `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("decode roadmap issues: %w", err)
		}

		for i := range resp.Issues.Nodes {
			all = append(all, resp.Issues.Nodes[i].toIssue())
		}

		if !resp.Issues.PageInfo.HasNextPage {
			break
		}
		cursor = &resp.Issues.PageInfo.EndCursor
	}

	return all, nil
}
