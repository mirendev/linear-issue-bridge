package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
	"miren.dev/linear-issue-bridge/internal/page"
)

type fakeIssues struct {
	issues map[string]*linearapi.Issue
	err    error
	stall  bool
}

func (f *fakeIssues) Get(ctx context.Context, identifier string) (*linearapi.Issue, error) {
	if f.stall {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.issues[identifier], nil
}

func issue(id, title string, labels ...string) *linearapi.Issue {
	ls := make([]linearapi.Label, len(labels))
	for i, l := range labels {
		ls[i] = linearapi.Label{Name: l}
	}
	return &linearapi.Issue{
		Identifier:  id,
		Title:       title,
		Description: title + " body text",
		State:       linearapi.State{Name: "Todo", Type: "unstarted"},
		Labels:      ls,
	}
}

func newTestHandlers(t *testing.T, cache issueGetter) http.Handler {
	t.Helper()
	renderer, err := page.NewRenderer("MIR", "", "https://linear.miren.garden")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	h := &issueHandlers{
		cache:      cache,
		renderer:   renderer,
		identifier: regexp.MustCompile(`^MIR-\d+$`),
		shellAfter: 20 * time.Millisecond,
		apiWait:    20 * time.Millisecond,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/issue/{identifier}", h.api)
	mux.HandleFunc("GET /{identifier}", h.page)
	return mux
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var testIssues = &fakeIssues{issues: map[string]*linearapi.Issue{
	"MIR-1": issue("MIR-1", "Public thing", "public"),
	"MIR-2": issue("MIR-2", "Internal thing"),
	"MIR-3": issue("MIR-3", "Sensitive thing", "public", "security"),
}}

func TestIssuePage(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		cache    issueGetter
		wantCode int
		want     string
		dontWant string
	}{
		{"public", "/MIR-1", testIssues, 200, "Public thing", "data-issue-shell"},
		{"lowercase", "/mir-1", testIssues, 200, "Public thing", ""},
		{"unlabeled is stub", "/MIR-2", testIssues, 200, "not currently shared publicly", "Internal thing"},
		{"security beats public", "/MIR-3", testIssues, 200, "not currently shared publicly", "Sensitive thing"},
		{"missing", "/MIR-9", testIssues, 404, "Issue not found", ""},
		{"bad identifier", "/ABC-1", testIssues, 404, "Issue not found", ""},
		{"linear slow", "/MIR-1", &fakeIssues{stall: true}, 200, `data-issue-shell="MIR-1"`, "Public thing"},
		{"linear erroring", "/MIR-1", &fakeIssues{err: errors.New("502")}, 200, `data-issue-shell="MIR-1"`, "Public thing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, newTestHandlers(t, tt.cache), tt.path)
			body := rec.Body.String()
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if !strings.Contains(body, tt.want) {
				t.Errorf("body missing %q", tt.want)
			}
			if tt.dontWant != "" && strings.Contains(body, tt.dontWant) {
				t.Errorf("body unexpectedly contains %q", tt.dontWant)
			}
		})
	}
}

func TestIssueAPI(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		cache     issueGetter
		wantCode  int
		wantState string
		dontWant  string
	}{
		{"public", "/api/issue/MIR-1", testIssues, 200, "issue", ""},
		{"unlabeled is stub", "/api/issue/MIR-2", testIssues, 200, "stub", "Internal thing"},
		{"security beats public", "/api/issue/MIR-3", testIssues, 200, "stub", "Sensitive thing"},
		{"missing", "/api/issue/MIR-9", testIssues, 200, "notfound", ""},
		{"bad identifier", "/api/issue/ABC-1", testIssues, 404, "", ""},
		{"linear slow", "/api/issue/MIR-1", &fakeIssues{stall: true}, 503, "", ""},
		{"linear erroring", "/api/issue/MIR-1", &fakeIssues{err: errors.New("502")}, 503, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, newTestHandlers(t, tt.cache), tt.path)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if tt.wantState == "" {
				return
			}
			var resp issueAPIResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.State != tt.wantState {
				t.Errorf("state = %q, want %q", resp.State, tt.wantState)
			}
			if tt.dontWant != "" && strings.Contains(resp.HTML+resp.Title, tt.dontWant) {
				t.Errorf("response leaks %q", tt.dontWant)
			}
		})
	}
}
