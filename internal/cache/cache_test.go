package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

type mockFetcher struct {
	mu    sync.Mutex
	issue *linearapi.Issue
	err   error
	// gate, when set, blocks every fetch until it is closed.
	gate  chan struct{}
	calls atomic.Int32
}

func (m *mockFetcher) set(issue *linearapi.Issue, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issue, m.err = issue, err
}

func (m *mockFetcher) FetchIssue(_ context.Context, _ string) (*linearapi.Issue, error) {
	m.calls.Add(1)
	m.mu.Lock()
	gate := m.gate
	m.mu.Unlock()
	if gate != nil {
		<-gate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.issue, m.err
}

func (m *mockFetcher) FetchPublicIssues(_ context.Context, _ string) ([]*linearapi.Issue, error) {
	return nil, nil
}

func (s *store[T]) peek(key string) (T, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		var zero T
		return zero, time.Time{}, false
	}
	return e.val, e.fetchedAt, true
}

// eventually polls cond until it holds or a second passes, for assertions about
// background refreshes.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition never became true")
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

func TestCacheServesStaleAndRefreshes(t *testing.T) {
	fetcher := &mockFetcher{issue: &linearapi.Issue{Identifier: "MIR-1", Title: "Old"}}
	c := New(fetcher, 1*time.Millisecond)

	if _, err := c.Get(context.Background(), "MIR-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	fetcher.set(&linearapi.Issue{Identifier: "MIR-1", Title: "New"}, nil)
	gate := make(chan struct{})
	fetcher.mu.Lock()
	fetcher.gate = gate
	fetcher.mu.Unlock()

	// The refresh is blocked, so this proves the stale read doesn't wait on it.
	got, err := c.Get(context.Background(), "MIR-1")
	if err != nil {
		t.Fatalf("Get (stale): %v", err)
	}
	if got.Title != "Old" {
		t.Errorf("Title = %q, want stale %q", got.Title, "Old")
	}

	close(gate)
	eventually(t, func() bool {
		got, _, _ := c.issues.peek("MIR-1")
		return got.Title == "New"
	})
}

func TestCacheFailedRefreshKeepsStale(t *testing.T) {
	fetcher := &mockFetcher{issue: &linearapi.Issue{Identifier: "MIR-1", Title: "Old"}}
	c := New(fetcher, 1*time.Millisecond)

	if _, err := c.Get(context.Background(), "MIR-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	fetcher.set(nil, errors.New("linear is down"))
	for range 3 {
		got, err := c.Get(context.Background(), "MIR-1")
		if err != nil {
			t.Fatalf("Get during outage: %v", err)
		}
		if got.Title != "Old" {
			t.Errorf("Title = %q, want %q", got.Title, "Old")
		}
	}
}

func TestCacheTooStaleWaits(t *testing.T) {
	fetcher := &mockFetcher{issue: &linearapi.Issue{Identifier: "MIR-1", Title: "Old"}}
	c := New(fetcher, 1*time.Millisecond)
	c.issues.maxStale = 2 * time.Millisecond

	if _, err := c.Get(context.Background(), "MIR-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	fetcher.set(nil, errors.New("linear is down"))
	if _, err := c.Get(context.Background(), "MIR-1"); err == nil {
		t.Fatal("expected error once past maxStale, got stale data")
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

func TestCacheSharesInflightFetch(t *testing.T) {
	fetcher := &mockFetcher{
		issue: &linearapi.Issue{Identifier: "MIR-1"},
		gate:  make(chan struct{}),
	}
	c := New(fetcher, 1*time.Minute)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := c.Get(context.Background(), "MIR-1"); err != nil {
				t.Errorf("Get: %v", err)
			}
		})
	}
	eventually(t, func() bool { return fetcher.calls.Load() == 1 })
	close(fetcher.gate)
	wg.Wait()

	if n := fetcher.calls.Load(); n != 1 {
		t.Errorf("fetcher called %d times, want 1", n)
	}
}

func TestCacheCallerTimeoutLeavesWarmCache(t *testing.T) {
	fetcher := &mockFetcher{
		issue: &linearapi.Issue{Identifier: "MIR-1"},
		gate:  make(chan struct{}),
	}
	c := New(fetcher, 1*time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := c.Get(ctx, "MIR-1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Get err = %v, want DeadlineExceeded", err)
	}

	close(fetcher.gate)
	eventually(t, func() bool {
		_, _, ok := c.issues.peek("MIR-1")
		return ok
	})
	if _, err := c.Get(context.Background(), "MIR-1"); err != nil {
		t.Fatalf("Get after fetch landed: %v", err)
	}
	if n := fetcher.calls.Load(); n != 1 {
		t.Errorf("fetcher called %d times, want 1", n)
	}
}

func TestCacheInvalidateDropsInflightResult(t *testing.T) {
	fetcher := &mockFetcher{gate: make(chan struct{})}
	c := New(fetcher, 1*time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, _ = c.Get(ctx, "MIR-1")

	c.issues.mu.Lock()
	inflight := c.issues.inflight["MIR-1"]
	c.issues.mu.Unlock()

	// The issue gets created while the "not found" answer is in flight.
	c.Invalidate("MIR-1")
	close(fetcher.gate)
	<-inflight.done

	if _, _, ok := c.issues.peek("MIR-1"); ok {
		t.Error("pre-invalidate fetch result was cached")
	}
}
