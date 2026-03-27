package main

import (
	"context"
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
	"miren.dev/linear-issue-bridge/internal/github"
	"miren.dev/linear-issue-bridge/internal/linearapi"
	"miren.dev/linear-issue-bridge/internal/page"
)

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

	apiKey := os.Getenv("LINEAR_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("LINEAR_API_KEY is required")
	}

	teamKey := os.Getenv("LINEAR_TEAM_KEY")
	if teamKey == "" {
		return fmt.Errorf("LINEAR_TEAM_KEY is required")
	}

	client := linearapi.NewClient(apiKey)
	issueCache := cache.New(client, cache.DefaultTTL)

	fathomSiteID := os.Getenv("FATHOM_SITE_ID")

	renderer, err := page.NewRenderer(teamKey, fathomSiteID)
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
		fmt.Fprint(w, "ok")
	})

	mux.Handle("GET /static/", http.StripPrefix("/static/", renderer.StaticHandler()))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if err := renderer.RenderIndexPage(w); err != nil {
			slog.Error("render index", "error", err)
		}
	})

	mux.HandleFunc("GET /issues", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		issues, err := issueCache.GetPublicIssues(ctx, teamKey)
		if err != nil {
			slog.Error("fetch public issues", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if err := renderer.RenderIssuesPage(w, issues); err != nil {
			slog.Error("render issues", "error", err)
		}
	})

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

		if title == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			renderer.RenderSuggestPage(w, page.SuggestPageData{
				Title:       title,
				Description: description,
				Error:       "Title is required.",
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		file, header, err := r.FormFile("attachment")
		if err == nil {
			defer file.Close()
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

		created, err := client.CreateIssue(ctx, teamID, title, description, []string{publicLabelID, userSubmittedLabelID})
		if err != nil {
			slog.Error("create issue", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			renderer.RenderSuggestPage(w, page.SuggestPageData{
				Title:       title,
				Description: description,
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

	mux.HandleFunc("GET /{identifier}", func(w http.ResponseWriter, r *http.Request) {
		identifier := strings.ToUpper(r.PathValue("identifier"))

		if !identifierPattern.MatchString(identifier) {
			w.WriteHeader(http.StatusNotFound)
			if err := renderer.RenderNotFound(w); err != nil {
				slog.Error("render not found", "error", err)
			}
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		issue, err := issueCache.Get(ctx, identifier)
		if err != nil {
			slog.Error("fetch issue", "identifier", identifier, "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if issue == nil {
			w.WriteHeader(http.StatusNotFound)
			if err := renderer.RenderNotFound(w); err != nil {
				slog.Error("render not found", "error", err)
			}
			return
		}

		if !issue.HasLabel("public") {
			w.WriteHeader(http.StatusOK)
			if err := renderer.RenderStubPage(w, identifier); err != nil {
				slog.Error("render stub", "error", err)
			}
			return
		}

		slog.Info("serving issue", "identifier", identifier)
		w.WriteHeader(http.StatusOK)
		if err := renderer.RenderIssuePage(w, issue); err != nil {
			slog.Error("render issue", "error", err)
		}
	})

	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	if webhookSecret != "" {
		labeler := linearapi.NewPublicLabeler(client, teamKey)
		webhookHandler := github.NewWebhookHandler(webhookSecret, teamKey, labeler)
		mux.Handle("POST /webhook/github", webhookHandler)
		slog.Info("github webhook enabled", "path", "/webhook/github")
	} else {
		slog.Info("github webhook disabled (GITHUB_WEBHOOK_SECRET not set)")
	}

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	slog.Info("starting server", "addr", "http://"+ln.Addr().String(), "team_key", teamKey)
	return http.Serve(ln, mux)
}
