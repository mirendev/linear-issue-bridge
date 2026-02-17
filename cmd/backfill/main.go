package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"miren.dev/linear-issue-bridge/internal/github"
	"miren.dev/linear-issue-bridge/internal/linearapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		apply  bool
		repo   string
		gitDir string
	)
	flag.BoolVar(&apply, "apply", false, "actually apply labels (default is dry-run)")
	flag.StringVar(&repo, "repo", "mirendev/runtime", "GitHub owner/repo to scan")
	flag.StringVar(&gitDir, "git-dir", ".", "local git clone to scan for commit messages")
	flag.Parse()

	apiKey := os.Getenv("LINEAR_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("LINEAR_API_KEY is required")
	}

	teamKey := os.Getenv("LINEAR_TEAM_KEY")
	if teamKey == "" {
		return fmt.Errorf("LINEAR_TEAM_KEY is required")
	}

	ghToken := os.Getenv("GITHUB_TOKEN")
	if ghToken == "" {
		ghToken = ghAuthToken()
	}

	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format %q, want owner/repo", repo)
	}

	ctx := context.Background()

	scanner := github.NewRepoScanner(ghToken, parts[0], parts[1])
	scanner.SetGitDir(gitDir)

	identifiers, err := scanner.ScanRepo(ctx, teamKey)
	if err != nil {
		return fmt.Errorf("scan repo: %w", err)
	}

	slog.Info("scan complete", "identifiers", len(identifiers))

	if !apply {
		fmt.Println("dry-run: would apply public label to:")
		for _, id := range identifiers {
			fmt.Printf("  %s\n", id)
		}
		fmt.Printf("\nre-run with -apply to label these issues\n")
		return nil
	}

	client := linearapi.NewClient(apiKey)
	labeler := linearapi.NewPublicLabeler(client, teamKey)

	for i, id := range identifiers {
		if err := labeler.EnsurePublicLabel(ctx, id); err != nil {
			return fmt.Errorf("label %s (%d/%d): %w", id, i+1, len(identifiers), err)
		}
	}

	slog.Info("backfill complete", "labeled", len(identifiers))
	return nil
}

func ghAuthToken() string {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
