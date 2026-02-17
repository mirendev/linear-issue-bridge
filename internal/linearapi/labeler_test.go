package linearapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicLabeler_IssueNotFound(t *testing.T) {
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
	labeler := NewPublicLabeler(client, "MIR")

	err := labeler.EnsurePublicLabel(context.Background(), "MIR-999")
	if err != nil {
		t.Fatalf("expected no error for missing issue, got: %v", err)
	}
}

func TestPublicLabeler_AlreadyLabeled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": []map[string]any{
						{
							"id":         "issue-uuid-1",
							"identifier": "MIR-42",
							"title":      "Test",
							"labels": map[string]any{
								"nodes": []map[string]any{
									{"id": "label-uuid-1", "name": "public", "color": "#5e6ad2"},
								},
							},
							"state":       map[string]any{"name": "Todo", "color": "#fff", "type": "unstarted"},
							"attachments": map[string]any{"nodes": []any{}},
							"createdAt":   "2025-01-15T10:00:00.000Z",
							"updatedAt":   "2025-01-15T10:00:00.000Z",
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
	labeler := NewPublicLabeler(client, "MIR")

	err := labeler.EnsurePublicLabel(context.Background(), "MIR-42")
	if err != nil {
		t.Fatalf("expected no error for already-labeled issue, got: %v", err)
	}
}

func TestPublicLabeler_NonpublicLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": map[string]any{
				"issues": map[string]any{
					"nodes": []map[string]any{
						{
							"id":         "issue-uuid-1",
							"identifier": "MIR-42",
							"title":      "Secret stuff",
							"labels": map[string]any{
								"nodes": []map[string]any{
									{"id": "label-uuid-1", "name": "nonpublic", "color": "#e55"},
								},
							},
							"state":       map[string]any{"name": "Todo", "color": "#fff", "type": "unstarted"},
							"attachments": map[string]any{"nodes": []any{}},
							"createdAt":   "2025-01-15T10:00:00.000Z",
							"updatedAt":   "2025-01-15T10:00:00.000Z",
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
	labeler := NewPublicLabeler(client, "MIR")

	err := labeler.EnsurePublicLabel(context.Background(), "MIR-42")
	if err != nil {
		t.Fatalf("expected no error for nonpublic issue, got: %v", err)
	}
}

func TestPublicLabeler_AppliesLabel(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req graphQLRequest
		json.NewDecoder(r.Body).Decode(&req)
		callCount++

		var resp any
		switch {
		case strings.Contains(req.Query, "IssueByIdentifier"):
			resp = map[string]any{
				"data": map[string]any{
					"issues": map[string]any{
						"nodes": []map[string]any{
							{
								"id":         "issue-uuid-1",
								"identifier": "MIR-42",
								"title":      "Test",
								"labels": map[string]any{
									"nodes": []any{},
								},
								"state":       map[string]any{"name": "Todo", "color": "#fff", "type": "unstarted"},
								"attachments": map[string]any{"nodes": []any{}},
								"createdAt":   "2025-01-15T10:00:00.000Z",
								"updatedAt":   "2025-01-15T10:00:00.000Z",
							},
						},
					},
				},
			}
		case strings.Contains(req.Query, "LabelByName"):
			resp = map[string]any{
				"data": map[string]any{
					"issueLabels": map[string]any{
						"nodes": []map[string]any{
							{"id": "label-uuid-public", "name": "public"},
						},
					},
				},
			}
		case strings.Contains(req.Query, "AddLabel"):
			if req.Variables["issueID"] != "issue-uuid-1" {
				t.Errorf("expected issueID 'issue-uuid-1', got %v", req.Variables["issueID"])
			}
			if req.Variables["labelID"] != "label-uuid-public" {
				t.Errorf("expected labelID 'label-uuid-public', got %v", req.Variables["labelID"])
			}
			resp = map[string]any{
				"data": map[string]any{
					"issueAddLabel": map[string]any{
						"success": true,
					},
				},
			}
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key")
	client.SetEndpoint(srv.URL)
	labeler := NewPublicLabeler(client, "MIR")

	err := labeler.EnsurePublicLabel(context.Background(), "MIR-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 3 {
		t.Errorf("expected 3 API calls (fetch issue, fetch label, add label), got %d", callCount)
	}
}

func TestPublicLabeler_FetchIssueError(t *testing.T) {
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
	labeler := NewPublicLabeler(client, "MIR")

	err := labeler.EnsurePublicLabel(context.Background(), "MIR-42")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
