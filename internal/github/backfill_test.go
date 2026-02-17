package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoScanner_ScanRepo(t *testing.T) {
	gitDir := initTestRepo(t,
		"fix MIR-1: broken thing",
		"no refs here",
		"close MIR-2 and MIR-3",
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/org/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"title": "MIR-4: new feature", "body": "implements MIR-5"},
		})
	})
	mux.HandleFunc("/repos/org/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"title": "bug report", "body": "related to MIR-1"},
		})
	})
	mux.HandleFunc("/repos/org/repo/issues/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"body": "see also MIR-6"},
		})
	})
	mux.HandleFunc("/repos/org/repo/pulls/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"body": "this relates to MIR-7 and OTHER-99"},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	scanner := NewRepoScanner("", "org", "repo")
	scanner.baseURL = srv.URL
	scanner.SetGitDir(gitDir)

	ids, err := scanner.ScanRepo(context.Background(), "MIR")
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	want := map[string]bool{
		"MIR-1": true, "MIR-2": true, "MIR-3": true,
		"MIR-4": true, "MIR-5": true, "MIR-6": true, "MIR-7": true,
	}

	if len(ids) != len(want) {
		t.Fatalf("got %d identifiers %v, want %d", len(ids), ids, len(want))
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected identifier %q", id)
		}
	}
}

func TestRepoScanner_GitLog(t *testing.T) {
	gitDir := initTestRepo(t,
		"MIR-10: first commit",
		"MIR-11: second commit",
		"unrelated work",
		"also references MIR-10 again",
	)

	mux := http.NewServeMux()
	emptyHandler := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{})
	}
	mux.HandleFunc("/repos/org/repo/pulls", emptyHandler)
	mux.HandleFunc("/repos/org/repo/issues", emptyHandler)
	mux.HandleFunc("/repos/org/repo/issues/comments", emptyHandler)
	mux.HandleFunc("/repos/org/repo/pulls/comments", emptyHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	scanner := NewRepoScanner("", "org", "repo")
	scanner.baseURL = srv.URL
	scanner.SetGitDir(gitDir)

	ids, err := scanner.ScanRepo(context.Background(), "MIR")
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	want := map[string]bool{"MIR-10": true, "MIR-11": true}
	if len(ids) != len(want) {
		t.Fatalf("got %d identifiers %v, want %d", len(ids), ids, len(want))
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected identifier %q", id)
		}
	}
}

func TestRepoScanner_NoGitDir(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/org/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"title": "MIR-1: feature", "body": ""},
		})
	})
	emptyHandler := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{})
	}
	mux.HandleFunc("/repos/org/repo/issues", emptyHandler)
	mux.HandleFunc("/repos/org/repo/issues/comments", emptyHandler)
	mux.HandleFunc("/repos/org/repo/pulls/comments", emptyHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	scanner := NewRepoScanner("", "org", "repo")
	scanner.baseURL = srv.URL

	ids, err := scanner.ScanRepo(context.Background(), "MIR")
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	if len(ids) != 1 || ids[0] != "MIR-1" {
		t.Fatalf("got %v, want [MIR-1]", ids)
	}
}

func TestRepoScanner_Pagination(t *testing.T) {
	page := 0
	mux := http.NewServeMux()
	var srvURL string

	mux.HandleFunc("/repos/org/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/org/repo/pulls?page=2&per_page=100>; rel="next"`, srvURL))
			json.NewEncoder(w).Encode([]map[string]string{
				{"title": "MIR-10", "body": ""},
			})
		} else {
			json.NewEncoder(w).Encode([]map[string]string{
				{"title": "MIR-11", "body": ""},
			})
		}
	})
	emptyHandler := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{})
	}
	mux.HandleFunc("/repos/org/repo/issues", emptyHandler)
	mux.HandleFunc("/repos/org/repo/issues/comments", emptyHandler)
	mux.HandleFunc("/repos/org/repo/pulls/comments", emptyHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	scanner := NewRepoScanner("", "org", "repo")
	scanner.baseURL = srv.URL

	ids, err := scanner.ScanRepo(context.Background(), "MIR")
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	want := map[string]bool{"MIR-10": true, "MIR-11": true}
	if len(ids) != len(want) {
		t.Fatalf("got %d identifiers %v, want %d", len(ids), ids, len(want))
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected identifier %q", id)
		}
	}
}

func TestRepoScanner_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/org/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"rate limited"}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	scanner := NewRepoScanner("", "org", "repo")
	scanner.baseURL = srv.URL

	_, err := scanner.ScanRepo(context.Background(), "MIR")
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestRepoScanner_AuthHeader(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/org/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode([]map[string]string{})
	})
	emptyHandler := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{})
	}
	mux.HandleFunc("/repos/org/repo/issues", emptyHandler)
	mux.HandleFunc("/repos/org/repo/issues/comments", emptyHandler)
	mux.HandleFunc("/repos/org/repo/pulls/comments", emptyHandler)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	scanner := NewRepoScanner("ghp_testtoken", "org", "repo")
	scanner.baseURL = srv.URL

	_, err := scanner.ScanRepo(context.Background(), "MIR")
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}
	if gotAuth != "Bearer ghp_testtoken" {
		t.Errorf("got auth %q, want %q", gotAuth, "Bearer ghp_testtoken")
	}
}

func TestNextPageURL(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{`<https://api.github.com/repos/org/repo/commits?page=2>; rel="next"`, "https://api.github.com/repos/org/repo/commits?page=2"},
		{`<https://api.github.com/repos/org/repo/commits?page=1>; rel="prev", <https://api.github.com/repos/org/repo/commits?page=3>; rel="next"`, "https://api.github.com/repos/org/repo/commits?page=3"},
		{`<https://api.github.com/repos/org/repo/commits?page=1>; rel="prev"`, ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := nextPageURL(tt.header)
		if got != tt.want {
			t.Errorf("nextPageURL(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func initTestRepo(t *testing.T, messages ...string) string {
	t.Helper()
	dir := t.TempDir()
	gitDir := filepath.Join(dir, "repo")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", gitDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	for i, msg := range messages {
		f := filepath.Join(gitDir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(f, []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-m", msg)
	}
	return gitDir
}
