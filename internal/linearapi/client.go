package linearapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultEndpoint = "https://api.linear.app/graphql"

type Client struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:   apiKey,
		endpoint: defaultEndpoint,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SetEndpoint overrides the GraphQL endpoint (useful for testing).
func (c *Client) SetEndpoint(endpoint string) {
	c.endpoint = endpoint
}

const issueByIdentifierQuery = `
query IssueByIdentifier($teamKey: String!, $number: Float!) {
  issues(
    filter: {
      team: { key: { eq: $teamKey } }
      number: { eq: $number }
    }
    first: 1
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
      state {
        name
        color
        type
      }
      labels {
        nodes {
          id
          name
          color
        }
      }
      attachments {
        nodes {
          url
          title
        }
      }
    }
  }
}
`

const labelByNameQuery = `
query LabelByName($teamKey: String!, $labelName: String!) {
  issueLabels(
    filter: {
      team: { key: { eq: $teamKey } }
      name: { eq: $labelName }
    }
    first: 1
  ) {
    nodes {
      id
      name
    }
  }
}
`

const addLabelMutation = `
mutation AddLabel($issueID: String!, $labelID: String!) {
  issueAddLabel(id: $issueID, labelId: $labelID) {
    success
  }
}
`

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type issuesResponse struct {
	Issues struct {
		Nodes []issueJSON `json:"nodes"`
	} `json:"issues"`
}

type issueJSON struct {
	ID          string    `json:"id"`
	Identifier  string    `json:"identifier"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Priority    int       `json:"priority"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	State       struct {
		Name  string `json:"name"`
		Color string `json:"color"`
		Type  string `json:"type"`
	} `json:"state"`
	Labels struct {
		Nodes []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"nodes"`
	} `json:"labels"`
	Attachments struct {
		Nodes []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"nodes"`
	} `json:"attachments"`
}

// ParseIdentifier splits "MIR-42" into ("MIR", 42).
func ParseIdentifier(identifier string) (teamKey string, number int, err error) {
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid identifier format: %s", identifier)
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid issue number in %s: %w", identifier, err)
	}
	return parts[0], n, nil
}

func (c *Client) do(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	reqBody := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linear API returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(respBytes, &gqlResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("linear API error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

// FetchIssue retrieves an issue by its identifier (e.g. "MIR-42").
// Returns nil, nil if the issue is not found.
func (c *Client) FetchIssue(ctx context.Context, identifier string) (*Issue, error) {
	teamKey, number, err := ParseIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	data, err := c.do(ctx, issueByIdentifierQuery, map[string]any{
		"teamKey": teamKey,
		"number":  float64(number),
	})
	if err != nil {
		return nil, err
	}

	var issueResp issuesResponse
	if err := json.Unmarshal(data, &issueResp); err != nil {
		return nil, fmt.Errorf("decode issue data: %w", err)
	}

	if len(issueResp.Issues.Nodes) == 0 {
		return nil, nil
	}

	return issueResp.Issues.Nodes[0].toIssue(), nil
}

// FetchLabelByName returns the UUID of a label by name within a team.
// Returns "", nil if the label is not found.
func (c *Client) FetchLabelByName(ctx context.Context, teamKey, name string) (string, error) {
	data, err := c.do(ctx, labelByNameQuery, map[string]any{
		"teamKey":   teamKey,
		"labelName": name,
	})
	if err != nil {
		return "", err
	}

	var resp struct {
		IssueLabels struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"issueLabels"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode label data: %w", err)
	}

	if len(resp.IssueLabels.Nodes) == 0 {
		return "", nil
	}

	return resp.IssueLabels.Nodes[0].ID, nil
}

// AddLabel appends a label to an issue.
func (c *Client) AddLabel(ctx context.Context, issueID, labelID string) error {
	_, err := c.do(ctx, addLabelMutation, map[string]any{
		"issueID": issueID,
		"labelID": labelID,
	})
	return err
}

func (j *issueJSON) toIssue() *Issue {
	labels := make([]Label, len(j.Labels.Nodes))
	for i, n := range j.Labels.Nodes {
		labels[i] = Label{ID: n.ID, Name: n.Name, Color: n.Color}
	}
	attachments := make([]Attachment, len(j.Attachments.Nodes))
	for i, n := range j.Attachments.Nodes {
		attachments[i] = Attachment{URL: n.URL, Title: n.Title}
	}
	return &Issue{
		ID:          j.ID,
		Identifier:  j.Identifier,
		Title:       j.Title,
		Description: j.Description,
		State:       State{Name: j.State.Name, Color: j.State.Color, Type: j.State.Type},
		Priority:    j.Priority,
		Labels:      labels,
		Attachments: attachments,
		URL:         j.URL,
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
	}
}
