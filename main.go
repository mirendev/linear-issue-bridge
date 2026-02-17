package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"miren.dev/linear-issue-bridge/internal/cache"
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

	renderer, err := page.NewRenderer(teamKey)
	if err != nil {
		return fmt.Errorf("initialize renderer: %w", err)
	}

	identifierPattern := regexp.MustCompile(`^` + regexp.QuoteMeta(strings.ToUpper(teamKey)) + `-\d+$`)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	mux.Handle("GET /static/", http.StripPrefix("/static/", renderer.StaticHandler()))

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

	addr := ":" + port
	slog.Info("starting server", "addr", addr, "team_key", teamKey)
	return http.ListenAndServe(addr, mux)
}
