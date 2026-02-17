package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockLabeler struct {
	called []string
	err    error
}

func (m *mockLabeler) EnsurePublicLabel(_ context.Context, identifier string) error {
	m.called = append(m.called, identifier)
	return m.err
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	handler := NewWebhookHandler("secret", "MIR", &mockLabeler{})

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestWebhookHandler_MissingSignature(t *testing.T) {
	handler := NewWebhookHandler("secret", "MIR", &mockLabeler{})

	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader("{}"))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestWebhookHandler_PushEvent(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"commits":[{"message":"Fix MIR-42 and MIR-7"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if len(mock.called) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(mock.called), mock.called)
	}
	if mock.called[0] != "MIR-42" {
		t.Errorf("called[0] = %q, want %q", mock.called[0], "MIR-42")
	}
	if mock.called[1] != "MIR-7" {
		t.Errorf("called[1] = %q, want %q", mock.called[1], "MIR-7")
	}
}

func TestWebhookHandler_PullRequestEvent(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"pull_request":{"title":"feat: MIR-10 add feature","body":"Resolves MIR-11"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "pull_request")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if len(mock.called) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(mock.called), mock.called)
	}
}

func TestWebhookHandler_IssuesEvent(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"issue":{"title":"Bug: MIR-5","body":"Details for MIR-5"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "issues")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	// MIR-5 appears in both title and body but should be deduplicated
	if len(mock.called) != 1 {
		t.Fatalf("expected 1 call, got %d: %v", len(mock.called), mock.called)
	}
	if mock.called[0] != "MIR-5" {
		t.Errorf("called[0] = %q, want %q", mock.called[0], "MIR-5")
	}
}

func TestWebhookHandler_IssueCommentEvent(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"comment":{"body":"See MIR-99"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "issue_comment")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if len(mock.called) != 1 {
		t.Fatalf("expected 1 call, got %d: %v", len(mock.called), mock.called)
	}
}

func TestWebhookHandler_TeamKeyFilter(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"commits":[{"message":"Fix ABC-1 and MIR-42"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if len(mock.called) != 1 {
		t.Fatalf("expected 1 call (only MIR-42), got %d: %v", len(mock.called), mock.called)
	}
	if mock.called[0] != "MIR-42" {
		t.Errorf("called[0] = %q, want %q", mock.called[0], "MIR-42")
	}
}

func TestWebhookHandler_PRReviewEvent(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"review":{"body":"This relates to MIR-33"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "pull_request_review")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if len(mock.called) != 1 {
		t.Fatalf("expected 1 call, got %d: %v", len(mock.called), mock.called)
	}
	if mock.called[0] != "MIR-33" {
		t.Errorf("called[0] = %q, want %q", mock.called[0], "MIR-33")
	}
}

func TestWebhookHandler_PRReviewCommentEvent(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"comment":{"body":"Nitpick on MIR-20 implementation"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "pull_request_review_comment")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if len(mock.called) != 1 {
		t.Fatalf("expected 1 call, got %d: %v", len(mock.called), mock.called)
	}
	if mock.called[0] != "MIR-20" {
		t.Errorf("called[0] = %q, want %q", mock.called[0], "MIR-20")
	}
}

func TestWebhookHandler_UnknownEvent(t *testing.T) {
	mock := &mockLabeler{}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"action":"completed"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "workflow_run")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if len(mock.called) != 0 {
		t.Errorf("expected 0 calls for unknown event, got %d", len(mock.called))
	}
}

func TestWebhookHandler_LabelerError(t *testing.T) {
	mock := &mockLabeler{err: fmt.Errorf("labeler broke")}
	handler := NewWebhookHandler("secret", "MIR", mock)

	body := `{"commits":[{"message":"MIR-1"}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("secret", body))
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Should still return 200 so GitHub doesn't retry
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (should return 200 even on labeler error)", rr.Code, http.StatusOK)
	}
}
