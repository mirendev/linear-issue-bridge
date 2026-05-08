package linearapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultEndpoint  = "https://api.linear.app/graphql"
	defaultTokenURL  = "https://api.linear.app/oauth/token"
	tokenExpiryBuffer = 5 * time.Minute
)

type Client struct {
	clientID     string
	clientSecret string
	endpoint     string
	tokenURL     string
	httpClient   *http.Client

	tokenMu     sync.Mutex
	token       string
	tokenExpiry time.Time
}

func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		endpoint:     defaultEndpoint,
		tokenURL:     defaultTokenURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}



// SetEndpoint overrides the GraphQL endpoint (useful for testing).
func (c *Client) SetEndpoint(endpoint string) {
	c.endpoint = endpoint
}

// SetTokenURL overrides the OAuth token endpoint (useful for testing).
func (c *Client) SetTokenURL(tokenURL string) {
	c.tokenURL = tokenURL
}

func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {"read,write"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	c.token = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn)*time.Second - tokenExpiryBuffer)

	return c.token, nil
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
      creator {
        displayName
        avatarUrl
      }
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
      relations {
        nodes {
          type
          relatedIssue {
            identifier
            title
          }
        }
      }
    }
  }
}
`

const labelByNameQuery = `
query LabelByName($labelName: String!) {
  issueLabels(
    filter: {
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

const publicIssuesQuery = `
query PublicIssues($teamKey: String!, $labelName: String!, $cursor: String) {
  issues(
    filter: {
      team: { key: { eq: $teamKey } }
      labels: { some: { name: { eq: $labelName } } }
      state: { type: { nin: ["completed", "cancelled"] } }
    }
    first: 50
    after: $cursor
    orderBy: updatedAt
  ) {
    nodes {
      id
      identifier
      title
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
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
`

const recentlyDonePublicIssuesQuery = `
query RecentlyDonePublicIssues($teamKey: String!, $labelName: String!, $since: DateTimeOrDuration!, $cursor: String) {
  issues(
    filter: {
      team: { key: { eq: $teamKey } }
      labels: { some: { name: { eq: $labelName } } }
      state: { type: { in: ["completed"] } }
      completedAt: { gte: $since }
    }
    first: 50
    after: $cursor
    orderBy: updatedAt
  ) {
    nodes {
      id
      identifier
      title
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
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
`

const recentlyCancelledPublicIssuesQuery = `
query RecentlyCancelledPublicIssues($teamKey: String!, $labelName: String!, $since: DateTimeOrDuration!, $cursor: String) {
  issues(
    filter: {
      team: { key: { eq: $teamKey } }
      labels: { some: { name: { eq: $labelName } } }
      state: { type: { in: ["cancelled"] } }
      canceledAt: { gte: $since }
    }
    first: 50
    after: $cursor
    orderBy: updatedAt
  ) {
    nodes {
      id
      identifier
      title
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
    }
    pageInfo {
      hasNextPage
      endCursor
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

const fileUploadMutation = `
mutation FileUpload($size: Int!, $contentType: String!, $filename: String!) {
  fileUpload(size: $size, contentType: $contentType, filename: $filename) {
    uploadFile {
      uploadUrl
      assetUrl
      headers {
        key
        value
      }
    }
  }
}
`

const teamByKeyQuery = `
query TeamByKey($teamKey: String!) {
  teams(filter: { key: { eq: $teamKey } }, first: 1) {
    nodes {
      id
    }
  }
}
`

const createIssueMutation = `
mutation CreateIssue($teamId: String!, $title: String!, $description: String, $labelIds: [String!]) {
  issueCreate(input: { teamId: $teamId, title: $title, description: $description, labelIds: $labelIds }) {
    success
    issue {
      id
      identifier
      title
    }
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
	Creator *struct {
		DisplayName string `json:"displayName"`
		AvatarURL   string `json:"avatarUrl"`
	} `json:"creator"`
	State struct {
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
	Relations struct {
		Nodes []struct {
			Type         string `json:"type"`
			RelatedIssue struct {
				Identifier string `json:"identifier"`
				Title      string `json:"title"`
			} `json:"relatedIssue"`
		} `json:"nodes"`
	} `json:"relations"`
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
	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}

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
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

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
func (c *Client) FetchLabelByName(ctx context.Context, _, name string) (string, error) {
	data, err := c.do(ctx, labelByNameQuery, map[string]any{
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

// FetchPublicIssues retrieves all open issues and recently completed issues
// (last 90 days) with the "public" label for a team.
func (c *Client) FetchPublicIssues(ctx context.Context, teamKey string) ([]*Issue, error) {
	var all []*Issue
	var cursor *string

	for {
		vars := map[string]any{
			"teamKey":   teamKey,
			"labelName": "public",
		}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		data, err := c.do(ctx, publicIssuesQuery, vars)
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
			return nil, fmt.Errorf("decode public issues: %w", err)
		}

		for i := range resp.Issues.Nodes {
			all = append(all, resp.Issues.Nodes[i].toIssue())
		}

		if !resp.Issues.PageInfo.HasNextPage {
			break
		}
		cursor = &resp.Issues.PageInfo.EndCursor
	}

	// Filter out issues that slipped through the GraphQL filter
	open := make([]*Issue, 0, len(all))
	for _, issue := range all {
		if issue.IsOpen() {
			open = append(open, issue)
		}
	}

	// Fetch recently completed issues (last 90 days)
	done, err := c.fetchRecentlyDonePublicIssues(ctx, teamKey)
	if err != nil {
		return nil, err
	}

	// Fetch recently cancelled/duplicate issues (last 90 days)
	cancelled, err := c.fetchRecentlyCancelledPublicIssues(ctx, teamKey)
	if err != nil {
		return nil, err
	}

	all = append(open, done...)
	all = append(all, cancelled...)
	SortByUpdatedDesc(all)

	return all, nil
}

func (c *Client) fetchRecentlyDonePublicIssues(ctx context.Context, teamKey string) ([]*Issue, error) {
	var all []*Issue
	var cursor *string
	since := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)

	for {
		vars := map[string]any{
			"teamKey":   teamKey,
			"labelName": "public",
			"since":     since,
		}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		data, err := c.do(ctx, recentlyDonePublicIssuesQuery, vars)
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
			return nil, fmt.Errorf("decode recently done issues: %w", err)
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

func (c *Client) fetchRecentlyCancelledPublicIssues(ctx context.Context, teamKey string) ([]*Issue, error) {
	var all []*Issue
	var cursor *string
	since := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)

	for {
		vars := map[string]any{
			"teamKey":   teamKey,
			"labelName": "public",
			"since":     since,
		}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		data, err := c.do(ctx, recentlyCancelledPublicIssuesQuery, vars)
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
			return nil, fmt.Errorf("decode recently cancelled issues: %w", err)
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

// FetchTeamID returns the UUID of a team by its key (e.g. "MIR").
func (c *Client) FetchTeamID(ctx context.Context, teamKey string) (string, error) {
	data, err := c.do(ctx, teamByKeyQuery, map[string]any{
		"teamKey": teamKey,
	})
	if err != nil {
		return "", err
	}

	var resp struct {
		Teams struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode team data: %w", err)
	}

	if len(resp.Teams.Nodes) == 0 {
		return "", fmt.Errorf("team %q not found", teamKey)
	}

	return resp.Teams.Nodes[0].ID, nil
}

type CreatedIssue struct {
	ID         string
	Identifier string
}

// CreateIssue creates a new issue in the given team with optional labels.
func (c *Client) CreateIssue(ctx context.Context, teamID, title, description string, labelIDs []string) (*CreatedIssue, error) {
	vars := map[string]any{
		"teamId":      teamID,
		"title":       title,
		"description": description,
	}
	if len(labelIDs) > 0 {
		vars["labelIds"] = labelIDs
	}
	data, err := c.do(ctx, createIssueMutation, vars)
	if err != nil {
		return nil, err
	}

	var resp struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode created issue: %w", err)
	}

	if !resp.IssueCreate.Success {
		return nil, fmt.Errorf("issue creation failed")
	}

	return &CreatedIssue{
		ID:         resp.IssueCreate.Issue.ID,
		Identifier: resp.IssueCreate.Issue.Identifier,
	}, nil
}

// UploadFile uploads a file to Linear's storage and returns the public asset URL.
func (c *Client) UploadFile(ctx context.Context, filename, contentType string, fileData []byte) (string, error) {
	data, err := c.do(ctx, fileUploadMutation, map[string]any{
		"size":        len(fileData),
		"contentType": contentType,
		"filename":    filename,
	})
	if err != nil {
		return "", fmt.Errorf("request upload URL: %w", err)
	}

	var resp struct {
		FileUpload struct {
			UploadFile struct {
				UploadURL string `json:"uploadUrl"`
				AssetURL  string `json:"assetUrl"`
				Headers   []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"headers"`
			} `json:"uploadFile"`
		} `json:"fileUpload"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}

	upload := resp.FileUpload.UploadFile

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, upload.UploadURL, bytes.NewReader(fileData))
	if err != nil {
		return "", fmt.Errorf("create upload request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	for _, h := range upload.Headers {
		req.Header.Set(h.Key, h.Value)
	}

	putResp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}
	defer func() { _ = putResp.Body.Close() }()

	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		body, _ := io.ReadAll(putResp.Body)
		return "", fmt.Errorf("upload returned %d: %s", putResp.StatusCode, string(body))
	}

	return upload.AssetURL, nil
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
	relations := make([]Relation, len(j.Relations.Nodes))
	for i, n := range j.Relations.Nodes {
		relations[i] = Relation{
			Type:              n.Type,
			RelatedIdentifier: n.RelatedIssue.Identifier,
			RelatedTitle:      n.RelatedIssue.Title,
		}
	}
	var creator Creator
	if j.Creator != nil {
		creator = Creator{DisplayName: j.Creator.DisplayName, AvatarURL: j.Creator.AvatarURL}
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
		Relations:   relations,
		URL:         j.URL,
		CreatedAt:   j.CreatedAt,
		UpdatedAt:   j.UpdatedAt,
		Creator:     creator,
	}
}
