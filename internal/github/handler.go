package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const maxBodySize = 1 << 20 // 1 MB

type Labeler interface {
	EnsurePublicLabel(ctx context.Context, identifier string) error
}

type WebhookHandler struct {
	secret  []byte
	teamKey string
	labeler Labeler
}

func NewWebhookHandler(secret, teamKey string, labeler Labeler) *WebhookHandler {
	return &WebhookHandler{
		secret:  []byte(secret),
		teamKey: teamKey,
		labeler: labeler,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if !h.verifySignature(body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	texts := extractTexts(eventType, body)

	var allText strings.Builder
	for _, t := range texts {
		allText.WriteString(t)
		allText.WriteByte('\n')
	}

	identifiers := ScanIdentifiers(allText.String())

	prefix := strings.ToUpper(h.teamKey) + "-"
	for _, id := range identifiers {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if err := h.labeler.EnsurePublicLabel(r.Context(), id); err != nil {
			slog.Error("failed to ensure public label", "identifier", id, "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) verifySignature(body []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sig, err := hex.DecodeString(signature[len("sha256="):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

func extractTexts(eventType string, body []byte) []string {
	switch eventType {
	case "push":
		return extractPushTexts(body)
	case "pull_request":
		return extractPullRequestTexts(body)
	case "issues":
		return extractIssueTexts(body)
	case "issue_comment":
		return extractIssueCommentTexts(body)
	case "pull_request_review":
		return extractPRReviewTexts(body)
	case "pull_request_review_comment":
		return extractPRReviewCommentTexts(body)
	default:
		return nil
	}
}

func extractPushTexts(body []byte) []string {
	var payload struct {
		Commits []struct {
			Message string `json:"message"`
		} `json:"commits"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	texts := make([]string, 0, len(payload.Commits))
	for _, c := range payload.Commits {
		texts = append(texts, c.Message)
	}
	return texts
}

func extractPullRequestTexts(body []byte) []string {
	var payload struct {
		PullRequest struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"pull_request"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	return []string{payload.PullRequest.Title, payload.PullRequest.Body}
}

func extractIssueTexts(body []byte) []string {
	var payload struct {
		Issue struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"issue"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	return []string{payload.Issue.Title, payload.Issue.Body}
}

func extractIssueCommentTexts(body []byte) []string {
	var payload struct {
		Comment struct {
			Body string `json:"body"`
		} `json:"comment"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	return []string{payload.Comment.Body}
}

func extractPRReviewTexts(body []byte) []string {
	var payload struct {
		Review struct {
			Body string `json:"body"`
		} `json:"review"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	return []string{payload.Review.Body}
}

func extractPRReviewCommentTexts(body []byte) []string {
	var payload struct {
		Comment struct {
			Body string `json:"body"`
		} `json:"comment"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	return []string{payload.Comment.Body}
}
