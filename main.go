package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"miren.dev/linear-issue-bridge/internal/cache"
	"miren.dev/linear-issue-bridge/internal/linearapi"
	"miren.dev/linear-issue-bridge/internal/page"
	"miren.dev/linear-issue-bridge/internal/roadmap"
	"miren.dev/linear-issue-bridge/internal/votes"
	"miren.dev/runtime/x/workloadid"
)

type issueListItem struct {
	Identifier     string `json:"identifier"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	StatusColor    string `json:"statusColor"`
	UpdatedAtISO   string `json:"updatedAtIso"`
	UpdatedDisplay string `json:"updatedDisplay"`
}

type issuesAPIResponse struct {
	Issues   []issueListItem `json:"issues"`
	Statuses []string        `json:"statuses"`
}

func buildIssuesResponse(issues []*linearapi.Issue) issuesAPIResponse {
	seen := make(map[string]bool)
	var statuses []string
	items := make([]issueListItem, 0, len(issues))
	for _, issue := range issues {
		display := issue.State.DisplayName()
		if !seen[display] {
			seen[display] = true
			statuses = append(statuses, display)
		}
		items = append(items, issueListItem{
			Identifier:     issue.Identifier,
			Title:          issue.Title,
			Status:         display,
			StatusColor:    issue.State.DisplayColor(),
			UpdatedAtISO:   issue.UpdatedAt.Format("2006-01-02"),
			UpdatedDisplay: issue.UpdatedAt.Format("Jan 2, 2006"),
		})
	}
	return issuesAPIResponse{Issues: items, Statuses: statuses}
}

// splitList reads a comma-separated env var, dropping blanks.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	clientID := os.Getenv("LINEAR_OAUTH_CLIENT_ID")
	if clientID == "" {
		return fmt.Errorf("LINEAR_OAUTH_CLIENT_ID is required")
	}
	clientSecret := os.Getenv("LINEAR_OAUTH_CLIENT_SECRET")
	if clientSecret == "" {
		return fmt.Errorf("LINEAR_OAUTH_CLIENT_SECRET is required")
	}

	client := linearapi.NewClient(clientID, clientSecret)

	teamKey := os.Getenv("LINEAR_TEAM_KEY")
	if teamKey == "" {
		return fmt.Errorf("LINEAR_TEAM_KEY is required")
	}
	issueCache := cache.New(client, cache.DefaultTTL)
	roadmapService := roadmap.NewService(client, roadmap.DefaultTTL)

	// The vote store is optional. Without a Valkey/Redis URL the roadmap still
	// renders and hearts are simply inert, which is what keeps a missing addon
	// a degraded feature rather than an outage.
	voteStore := votes.New(nil)
	if url := firstNonEmpty(os.Getenv("REDIS_URL"), os.Getenv("VALKEY_URL")); url != "" {
		be, err := votes.NewRedisBackend(url)
		if err != nil {
			return fmt.Errorf("configure vote store: %w", err)
		}
		voteStore = votes.New(be)
		defer func() { _ = voteStore.Close() }()
		slog.Info("vote store enabled")
	} else {
		slog.Info("vote store disabled (no REDIS_URL/VALKEY_URL)")
	}

	fathomSiteID := os.Getenv("FATHOM_SITE_ID")

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "https://linear.miren.garden"
	}

	renderer, err := page.NewRenderer(teamKey, fathomSiteID, baseURL)
	if err != nil {
		return fmt.Errorf("initialize renderer: %w", err)
	}

	ctx0, cancel0 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel0()
	teamID, err := client.FetchTeamID(ctx0, teamKey)
	if err != nil {
		return fmt.Errorf("fetch team ID for %s: %w", teamKey, err)
	}
	slog.Info("resolved team", "key", teamKey, "id", teamID)

	publicLabelID, err := client.FetchLabelByName(ctx0, teamKey, "public")
	if err != nil {
		return fmt.Errorf("fetch public label: %w", err)
	}
	if publicLabelID == "" {
		return fmt.Errorf("label %q not found — create it in Linear first", "public")
	}
	slog.Info("resolved label", "name", "public", "id", publicLabelID)

	userSubmittedLabelID, err := client.FetchLabelByName(ctx0, teamKey, "user-submitted")
	if err != nil {
		return fmt.Errorf("fetch user-submitted label: %w", err)
	}
	if userSubmittedLabelID == "" {
		return fmt.Errorf("label %q not found — create it in Linear first", "user-submitted")
	}
	slog.Info("resolved label", "name", "user-submitted", "id", userSubmittedLabelID)

	identifierPattern := regexp.MustCompile(`^` + regexp.QuoteMeta(strings.ToUpper(teamKey)) + `-\d+$`)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	mux.Handle("GET /static/", http.StripPrefix("/static/", renderer.StaticHandler()))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if err := renderer.RenderIndexPage(w); err != nil {
			slog.Error("render index", "error", err)
		}
	})

	mux.HandleFunc("GET /issues", func(w http.ResponseWriter, r *http.Request) {
		if err := renderer.RenderIssuesPage(w); err != nil {
			slog.Error("render issues", "error", err)
		}
	})

	mux.HandleFunc("GET /api/issues", func(w http.ResponseWriter, r *http.Request) {
		// Outer handler timeout deliberately exceeds the Linear client's worst
		// case for one read (5s, then a 10s retry; see linearapi.Client.do),
		// so a slow upstream call fails with a real error before the request
		// context cancels out from under it.
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		// The cache already serves a recent-enough copy through a Linear
		// outage, so an error here means there is nothing safe to show.
		issues, err := issueCache.GetPublicIssues(ctx, teamKey)
		if err != nil {
			slog.Error("fetch public issues", "error", err)
			http.Error(w, "Linear unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(buildIssuesResponse(issues))
	})

	issueHandlers := &issueHandlers{
		cache:      issueCache,
		renderer:   renderer,
		identifier: identifierPattern,
		shellAfter: shellAfter,
		apiWait:    apiIssueWait,
	}
	mux.HandleFunc("GET /api/issue/{identifier}", issueHandlers.api)

	mux.HandleFunc("GET /api/roadmap", roadmap.BoardHandler(roadmapService, voteStore))

	// Voting is a write, so it is mounted only when we can tell who is calling.
	// A browser cannot hold a workload identity, so the website proxies the
	// click and proves it is the website; without a verifier configured the
	// route simply does not exist, rather than existing unauthenticated.
	if issuers := splitList(os.Getenv("ROADMAP_TRUSTED_ISSUERS")); len(issuers) > 0 {
		audience := firstNonEmpty(os.Getenv("ROADMAP_VOTE_AUDIENCE"), baseURL)
		verifier, err := workloadid.NewVerifier(workloadid.VerifierConfig{
			TrustedIssuers:      issuers,
			Audience:            audience,
			RequireOrganization: os.Getenv("ROADMAP_REQUIRE_ORGANIZATION"),
		})
		if err != nil {
			return fmt.Errorf("configure roadmap vote auth: %w", err)
		}
		mux.HandleFunc("POST /api/roadmap/vote", roadmap.VoteHandler(roadmapService, voteStore, voteAuth{verifier}))
		slog.Info("roadmap voting enabled", "audience", audience, "trusted_issuers", issuers)
	} else {
		slog.Info("roadmap voting disabled (ROADMAP_TRUSTED_ISSUERS not set)")
	}

	mux.HandleFunc("GET /suggest", func(w http.ResponseWriter, r *http.Request) {
		if err := renderer.RenderSuggestPage(w, page.SuggestPageData{}); err != nil {
			slog.Error("render suggest", "error", err)
		}
	})

	mux.HandleFunc("POST /suggest", func(w http.ResponseWriter, r *http.Request) {
		const maxUploadSize = 10 << 20 // 10 MB
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		if r.FormValue("website") != "" {
			http.Redirect(w, r, "/suggest", http.StatusSeeOther)
			return
		}

		title := strings.TrimSpace(r.FormValue("title"))
		description := strings.TrimSpace(r.FormValue("description"))
		contact := strings.TrimSpace(r.FormValue("contact"))

		if title == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = renderer.RenderSuggestPage(w, page.SuggestPageData{
				Title:       title,
				Description: description,
				Contact:     contact,
				Error:       "Title is required.",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		file, header, err := r.FormFile("attachment")
		if err == nil {
			defer func() { _ = file.Close() }()
			fileData, err := io.ReadAll(io.LimitReader(file, maxUploadSize))
			if err != nil {
				slog.Error("read attachment", "error", err)
			} else {
				contentType := header.Header.Get("Content-Type")
				if contentType == "" {
					contentType = "application/octet-stream"
				}
				assetURL, err := client.UploadFile(ctx, header.Filename, contentType, fileData)
				if err != nil {
					slog.Error("upload attachment", "error", err)
				} else if strings.HasPrefix(contentType, "image/") {
					description += "\n\n![" + header.Filename + "](" + assetURL + ")"
				} else {
					description += "\n\n[" + header.Filename + "](" + assetURL + ")"
				}
			}
		}

		if contact != "" {
			description += "\n\n**Contact:** " + contact
		}

		created, err := client.CreateIssue(ctx, teamID, title, description, []string{publicLabelID, userSubmittedLabelID})
		if err != nil {
			slog.Error("create issue", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = renderer.RenderSuggestPage(w, page.SuggestPageData{
				Title:       title,
				Description: description,
				Contact:     contact,
				Error:       "Something went wrong. Please try again.",
			})
			return
		}

		issueCache.Invalidate(created.Identifier)
		slog.Info("suggestion created", "identifier", created.Identifier)
		if err := renderer.RenderSuggestPage(w, page.SuggestPageData{
			Success:    true,
			Identifier: created.Identifier,
		}); err != nil {
			slog.Error("render suggest success", "error", err)
		}
	})

	mux.HandleFunc("GET /{identifier}", issueHandlers.page)

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	slog.Info("starting server", "addr", "http://"+ln.Addr().String(), "team_key", teamKey)
	return http.Serve(ln, mux)
}

// voteAuth adapts a workloadid.Verifier to the roadmap handler's
// Authenticate shape: a vote is accepted only with a bearer token from a
// trusted cluster, minted for this service.
type voteAuth struct{ v *workloadid.Verifier }

func (a voteAuth) Authenticate(ctx context.Context, r *http.Request) error {
	token, err := workloadid.BearerToken(r)
	if err != nil {
		return err
	}
	_, err = a.v.Verify(ctx, token)
	return err
}
