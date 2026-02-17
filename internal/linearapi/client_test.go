package linearapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseIdentifier(t *testing.T) {
	tests := []struct {
		input   string
		teamKey string
		number  int
		wantErr bool
	}{
		{"MIR-42", "MIR", 42, false},
		{"ABC-1", "ABC", 1, false},
		{"MIR-0", "MIR", 0, false},
		{"NOSPACE", "", 0, true},
		{"MIR-abc", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			teamKey, number, err := ParseIdentifier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseIdentifier(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if teamKey != tt.teamKey {
				t.Errorf("teamKey = %q, want %q", teamKey, tt.teamKey)
			}
			if number != tt.number {
				t.Errorf("number = %d, want %d", number, tt.number)
			}
		})
	}
}

func TestFetchIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "test-key" {
			t.Errorf("expected Authorization header 'test-key', got %q", r.Header.Get("Authorization"))
		}

		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": []map[string]any{
						{
							"identifier":  "MIR-42",
							"title":       "Test Issue",
							"description": "A test description",
							"url":         "https://linear.app/miren/issue/MIR-42",
							"priority":    2,
							"createdAt":   "2025-01-15T10:00:00.000Z",
							"updatedAt":   "2025-01-15T12:00:00.000Z",
							"state": map[string]any{
								"name":  "In Progress",
								"color": "#f2c94c",
								"type":  "started",
							},
							"labels": map[string]any{
								"nodes": []map[string]any{
									{"name": "public", "color": "#5e6ad2"},
									{"name": "bug", "color": "#eb5757"},
								},
							},
							"attachments": map[string]any{
								"nodes": []map[string]any{
									{"url": "https://github.com/mirendev/linear-issue-bridge/pull/1", "title": "feat: add PR links"},
									{"url": "https://linear.app/some-other-link", "title": "Other"},
								},
							},
						},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key")
	client.SetEndpoint(srv.URL)

	issue, err := client.FetchIssue(context.Background(), "MIR-42")
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if issue == nil {
		t.Fatal("expected issue, got nil")
	}
	if issue.Identifier != "MIR-42" {
		t.Errorf("Identifier = %q, want %q", issue.Identifier, "MIR-42")
	}
	if issue.Title != "Test Issue" {
		t.Errorf("Title = %q, want %q", issue.Title, "Test Issue")
	}
	if issue.State.Name != "In Progress" {
		t.Errorf("State.Name = %q, want %q", issue.State.Name, "In Progress")
	}
	if len(issue.Labels) != 2 {
		t.Fatalf("Labels count = %d, want 2", len(issue.Labels))
	}
	if !issue.HasLabel("public") {
		t.Error("expected issue to have 'public' label")
	}
	if len(issue.Attachments) != 2 {
		t.Fatalf("Attachments count = %d, want 2", len(issue.Attachments))
	}
	prs := issue.GitHubPRs()
	if len(prs) != 1 {
		t.Fatalf("GitHubPRs count = %d, want 1", len(prs))
	}
	if prs[0].Title != "feat: add PR links" {
		t.Errorf("PR title = %q, want %q", prs[0].Title, "feat: add PR links")
	}
}

func TestFetchIssueNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": []any{},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key")
	client.SetEndpoint(srv.URL)

	issue, err := client.FetchIssue(context.Background(), "MIR-999")
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if issue != nil {
		t.Errorf("expected nil issue, got %+v", issue)
	}
}

func TestFetchIssueGraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data":   nil,
			"errors": []map[string]any{{"message": "something went wrong"}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key")
	client.SetEndpoint(srv.URL)

	_, err := client.FetchIssue(context.Background(), "MIR-42")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
