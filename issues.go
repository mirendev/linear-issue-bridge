package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
	"miren.dev/linear-issue-bridge/internal/page"
)

const (
	// shellAfter is how long an issue page waits on Linear before giving up
	// and serving the shell. Long enough for a normal fetch (100-300ms) to
	// render the full page, which is what link unfurlers see; short enough
	// that a stalled Linear never looks like a hung site.
	shellAfter = 1500 * time.Millisecond

	// apiIssueWait bounds how long the shell's fetch waits on Linear. It
	// covers a stalled first attempt plus the client's one retry; past that
	// the browser shows the failure and retries on its own schedule.
	apiIssueWait = 16 * time.Second
)

type issueAPIResponse struct {
	// State is "issue", "stub", or "notfound".
	State string `json:"state"`
	page.Fragment
}

type issueGetter interface {
	Get(ctx context.Context, identifier string) (*linearapi.Issue, error)
}

// issueHandlers serve a single issue, both as a page and as the JSON the
// page's shell fills itself in from. Both routes must agree on what is
// public: only IsPublic issues ever render beyond the stub.
type issueHandlers struct {
	cache      issueGetter
	renderer   *page.Renderer
	identifier *regexp.Regexp
	shellAfter time.Duration
	apiWait    time.Duration
}

func (h *issueHandlers) page(w http.ResponseWriter, r *http.Request) {
	identifier := strings.ToUpper(r.PathValue("identifier"))

	if !h.identifier.MatchString(identifier) {
		w.WriteHeader(http.StatusNotFound)
		if err := h.renderer.RenderNotFound(w); err != nil {
			slog.Error("render not found", "error", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.shellAfter)
	issue, err := h.cache.Get(ctx, identifier)
	cancel()
	if err != nil {
		// The fetch carries on in the background; the shell's request
		// will pick up its result or keep trying.
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("linear slow, serving issue shell", "identifier", identifier)
		} else {
			slog.Error("fetch issue, serving issue shell", "identifier", identifier, "error", err)
		}
		if err := h.renderer.RenderIssueShell(w, identifier); err != nil {
			slog.Error("render issue shell", "error", err)
		}
		return
	}

	if issue == nil {
		w.WriteHeader(http.StatusNotFound)
		if err := h.renderer.RenderNotFound(w); err != nil {
			slog.Error("render not found", "error", err)
		}
		return
	}

	if !issue.IsPublic() {
		w.WriteHeader(http.StatusOK)
		if err := h.renderer.RenderStubPage(w, identifier); err != nil {
			slog.Error("render stub", "error", err)
		}
		return
	}

	slog.Info("serving issue", "identifier", identifier)
	w.WriteHeader(http.StatusOK)
	if err := h.renderer.RenderIssuePage(w, issue); err != nil {
		slog.Error("render issue", "error", err)
	}
}

func (h *issueHandlers) api(w http.ResponseWriter, r *http.Request) {
	identifier := strings.ToUpper(r.PathValue("identifier"))
	w.Header().Set("Cache-Control", "no-store")
	if !h.identifier.MatchString(identifier) {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.apiWait)
	defer cancel()

	issue, err := h.cache.Get(ctx, identifier)
	if err != nil {
		slog.Error("fetch issue", "identifier", identifier, "error", err)
		http.Error(w, "Linear unavailable", http.StatusServiceUnavailable)
		return
	}

	var resp issueAPIResponse
	switch {
	case issue == nil:
		resp.State = "notfound"
		resp.Fragment, err = h.renderer.NotFoundFragment()
	case !issue.IsPublic():
		resp.State = "stub"
		resp.Fragment, err = h.renderer.StubFragment(identifier)
	default:
		resp.State = "issue"
		resp.Fragment, err = h.renderer.IssueFragment(issue)
	}
	if err != nil {
		slog.Error("render issue fragment", "identifier", identifier, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}
