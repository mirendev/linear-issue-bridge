package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

type mockFetcher struct {
	issue *linearapi.Issue
	err   error
	calls atomic.Int32
}

func (m *mockFetcher) FetchIssue(_ context.Context, _ string) (*linearapi.Issue, error) {
	m.calls.Add(1)
	return m.issue, m.err
}

func TestCacheHit(t *testing.T) {
	issue := &linearapi.Issue{Identifier: "MIR-1", Title: "Cached"}
	fetcher := &mockFetcher{issue: issue}
	c := New(fetcher, 1*time.Minute)

	got, err := c.Get(context.Background(), "MIR-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Identifier != "MIR-1" {
		t.Errorf("Identifier = %q, want %q", got.Identifier, "MIR-1")
	}

	got2, err := c.Get(context.Background(), "MIR-1")
	if err != nil {
		t.Fatalf("Get (cached): %v", err)
	}
	if got2.Identifier != "MIR-1" {
		t.Errorf("Identifier = %q, want %q", got2.Identifier, "MIR-1")
	}

	if fetcher.calls.Load() != 1 {
		t.Errorf("fetcher called %d times, want 1", fetcher.calls.Load())
	}
}

func TestCacheExpiry(t *testing.T) {
	issue := &linearapi.Issue{Identifier: "MIR-1", Title: "Expiring"}
	fetcher := &mockFetcher{issue: issue}
	c := New(fetcher, 1*time.Millisecond)

	_, err := c.Get(context.Background(), "MIR-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	_, err = c.Get(context.Background(), "MIR-1")
	if err != nil {
		t.Fatalf("Get (expired): %v", err)
	}

	if fetcher.calls.Load() != 2 {
		t.Errorf("fetcher called %d times, want 2", fetcher.calls.Load())
	}
}

func TestCacheFetchError(t *testing.T) {
	fetcher := &mockFetcher{err: errors.New("network error")}
	c := New(fetcher, 1*time.Minute)

	_, err := c.Get(context.Background(), "MIR-1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCacheNilIssue(t *testing.T) {
	fetcher := &mockFetcher{issue: nil}
	c := New(fetcher, 1*time.Minute)

	got, err := c.Get(context.Background(), "MIR-999")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	// Nil results should also be cached
	_, _ = c.Get(context.Background(), "MIR-999")
	if fetcher.calls.Load() != 1 {
		t.Errorf("fetcher called %d times, want 1 (nil should be cached)", fetcher.calls.Load())
	}
}
