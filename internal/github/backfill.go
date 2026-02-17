package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

type RepoScanner struct {
	baseURL string
	token   string
	owner   string
	repo    string
	gitDir  string
}

func NewRepoScanner(token, owner, repo string) *RepoScanner {
	return &RepoScanner{
		baseURL: "https://api.github.com",
		token:   token,
		owner:   owner,
		repo:    repo,
	}
}

func (s *RepoScanner) SetGitDir(dir string) {
	s.gitDir = dir
}

func (s *RepoScanner) ScanRepo(ctx context.Context, teamKey string) ([]string, error) {
	prefix := strings.ToUpper(teamKey) + "-"
	seen := make(map[string]bool)
	var result []string

	collect := func(text string) {
		for _, id := range ScanIdentifiers(text) {
			if strings.HasPrefix(id, prefix) && !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}

	before := 0

	if s.gitDir != "" {
		slog.Info("scanning git log", "dir", s.gitDir)
		if err := s.scanGitLog(ctx, collect); err != nil {
			return nil, fmt.Errorf("scan git log: %w", err)
		}
		slog.Info("finished git log", "new_ids", len(result)-before, "total_ids", len(result))
		before = len(result)
	}

	scanners := []struct {
		name string
		fn   func(ctx context.Context, collect func(string)) error
	}{
		{"pull requests", s.scanPullRequests},
		{"issues", s.scanIssues},
		{"issue comments", s.scanIssueComments},
		{"review comments", s.scanReviewComments},
	}

	for _, sc := range scanners {
		slog.Info("scanning", "source", sc.name)
		if err := sc.fn(ctx, collect); err != nil {
			return nil, fmt.Errorf("scan %s: %w", sc.name, err)
		}
		slog.Info("finished", "source", sc.name, "new_ids", len(result)-before, "total_ids", len(result))
		before = len(result)
	}

	return result, nil
}

func (s *RepoScanner) scanGitLog(ctx context.Context, collect func(string)) error {
	cmd := exec.CommandContext(ctx, "git", "-C", s.gitDir, "log", "--format=%B")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git log: %w", err)
	}
	collect(string(out))
	return nil
}

func (s *RepoScanner) scanPullRequests(ctx context.Context, collect func(string)) error {
	var prs []struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	return s.paginate(ctx, "pull requests", s.repoURL("/pulls?state=all"), func(body []byte) (int, error) {
		if err := json.Unmarshal(body, &prs); err != nil {
			return 0, err
		}
		for _, pr := range prs {
			collect(pr.Title)
			collect(pr.Body)
		}
		n := len(prs)
		prs = prs[:0]
		return n, nil
	})
}

func (s *RepoScanner) scanIssues(ctx context.Context, collect func(string)) error {
	var issues []struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	return s.paginate(ctx, "issues", s.repoURL("/issues?state=all"), func(body []byte) (int, error) {
		if err := json.Unmarshal(body, &issues); err != nil {
			return 0, err
		}
		for _, issue := range issues {
			collect(issue.Title)
			collect(issue.Body)
		}
		n := len(issues)
		issues = issues[:0]
		return n, nil
	})
}

func (s *RepoScanner) scanIssueComments(ctx context.Context, collect func(string)) error {
	var comments []struct {
		Body string `json:"body"`
	}
	return s.paginate(ctx, "issue comments", s.repoURL("/issues/comments"), func(body []byte) (int, error) {
		if err := json.Unmarshal(body, &comments); err != nil {
			return 0, err
		}
		for _, c := range comments {
			collect(c.Body)
		}
		n := len(comments)
		comments = comments[:0]
		return n, nil
	})
}

func (s *RepoScanner) scanReviewComments(ctx context.Context, collect func(string)) error {
	var comments []struct {
		Body string `json:"body"`
	}
	return s.paginate(ctx, "review comments", s.repoURL("/pulls/comments"), func(body []byte) (int, error) {
		if err := json.Unmarshal(body, &comments); err != nil {
			return 0, err
		}
		for _, c := range comments {
			collect(c.Body)
		}
		n := len(comments)
		comments = comments[:0]
		return n, nil
	})
}

func (s *RepoScanner) repoURL(path string) string {
	return fmt.Sprintf("%s/repos/%s/%s%s", s.baseURL, s.owner, s.repo, path)
}

func (s *RepoScanner) paginate(ctx context.Context, source, url string, decode func([]byte) (int, error)) error {
	if !strings.Contains(url, "per_page=") {
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		url += sep + "per_page=100"
	}

	page := 0
	total := 0
	for url != "" {
		page++
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if s.token != "" {
			req.Header.Set("Authorization", "Bearer "+s.token)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GitHub API %s: %s", resp.Status, body)
		}

		n, err := decode(body)
		if err != nil {
			return err
		}
		total += n

		url = nextPageURL(resp.Header.Get("Link"))
		if url != "" {
			slog.Info("fetching next page", "source", source, "page", page+1, "items_so_far", total)
		}
	}
	return nil
}

var linkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextPageURL(linkHeader string) string {
	m := linkNextRe.FindStringSubmatch(linkHeader)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
