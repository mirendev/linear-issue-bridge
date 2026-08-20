package roadmap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

type fakeFetcher struct {
	calls   atomic.Int32
	delay   time.Duration
	err     error
	release chan struct{}
}

func (f *fakeFetcher) FetchRoadmapProjects(ctx context.Context, _ []string) ([]*linearapi.Project, error) {
	f.calls.Add(1)
	if f.release != nil {
		<-f.release
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return nil, f.err
	}
	return projects(), nil
}

func (f *fakeFetcher) FetchRoadmapIssues(context.Context, []string) ([]*linearapi.Issue, error) {
	if f.err != nil {
		return nil, f.err
	}
	return issues(), nil
}

func TestServiceCachesWithinTTL(t *testing.T) {
	f := &fakeFetcher{}
	s := NewService(f, time.Minute)
	ctx := context.Background()

	if _, _, err := s.Board(ctx); err != nil {
		t.Fatalf("first Board() err = %v", err)
	}
	if _, stale, err := s.Board(ctx); err != nil || stale {
		t.Fatalf("second Board() stale=%v err=%v", stale, err)
	}
	if got := f.calls.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1", got)
	}
}

// Concurrent cold callers must share one Linear query, not start a stampede.
func TestServiceCoalescesConcurrentFetches(t *testing.T) {
	f := &fakeFetcher{release: make(chan struct{})}
	s := NewService(f, time.Minute)

	const callers = 8
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			_, _, _ = s.Board(context.Background())
		}()
	}

	// Let every caller reach the service before the fetch completes.
	time.Sleep(50 * time.Millisecond)
	close(f.release)
	wg.Wait()

	if got := f.calls.Load(); got != 1 {
		t.Errorf("fetched %d times under %d concurrent callers, want 1", got, callers)
	}
}

// A failure with nothing cached is an honest error, not an empty board
// pretending to be real.
func TestServiceErrorsWithNothingCached(t *testing.T) {
	f := &fakeFetcher{err: errors.New("linear down")}
	s := NewService(f, time.Minute)

	if _, _, err := s.Board(context.Background()); err == nil {
		t.Error("Board() succeeded with no data and a failing fetcher")
	}
}

// After a good fetch, a failure serves the last good board marked stale.
func TestServiceServesStaleAfterFailure(t *testing.T) {
	f := &fakeFetcher{}
	s := NewService(f, time.Millisecond)
	ctx := context.Background()

	if _, _, err := s.Board(ctx); err != nil {
		t.Fatalf("warmup err = %v", err)
	}
	time.Sleep(5 * time.Millisecond) // expire the TTL

	f.err = errors.New("linear down")
	board, stale, err := s.Board(ctx)
	if err != nil {
		t.Fatalf("Board() err = %v, want the stale board", err)
	}
	if !stale {
		t.Error("Board() reported fresh, want stale")
	}
	if len(board.InProgress) == 0 {
		t.Error("stale board came back empty")
	}
}

// Within the backoff window a failing upstream must not be retried per request.
func TestServiceBacksOffAfterFailure(t *testing.T) {
	f := &fakeFetcher{err: errors.New("linear down")}
	s := NewService(f, time.Millisecond)
	ctx := context.Background()

	_, _, _ = s.Board(ctx)
	first := f.calls.Load()

	for range 5 {
		_, _, _ = s.Board(ctx)
	}
	if got := f.calls.Load(); got != first {
		t.Errorf("retried during backoff: %d calls, want %d", got, first)
	}
}

func TestWithVotesMergesCounts(t *testing.T) {
	board := Build(projects(), issues(), time.Now())
	id := board.InProgress[0].ID

	merged := WithVotes(board, map[string]int{id: 42})

	if got := merged.InProgress[0].Votes; got != 42 {
		t.Errorf("votes = %d, want 42", got)
	}
	// The original must not be mutated: it is the shared cached value.
	if got := board.InProgress[0].Votes; got != 0 {
		t.Errorf("cached board was mutated, votes = %d", got)
	}
}

// Shipped cards are not votable, so their ids must not be accepted for writes.
func TestOpenIDsExcludesShipped(t *testing.T) {
	board := Build(projects(), issues(), time.Now())
	open := OpenIDs(board)

	if len(board.Shipped) == 0 {
		t.Fatal("fixture has no shipped cards to check against")
	}
	for _, c := range board.Shipped {
		if contains(open, c.ID) {
			t.Errorf("shipped card %q is votable", c.ID)
		}
	}
	for _, c := range board.InProgress {
		if !contains(open, c.ID) {
			t.Errorf("in-progress card %q is not votable", c.ID)
		}
	}
}

// A fetcher that returns nothing, so every column comes back empty.
type emptyFetcher struct{}

func (emptyFetcher) FetchRoadmapProjects(context.Context, []string) ([]*linearapi.Project, error) {
	return nil, nil
}
func (emptyFetcher) FetchRoadmapIssues(context.Context, []string) ([]*linearapi.Issue, error) {
	return nil, nil
}

// A caller that disconnects mid-fetch must not open the shared backoff for
// everyone else.
func TestServiceIgnoresCallerCancellationForBackoff(t *testing.T) {
	f := &fakeFetcher{release: make(chan struct{})}
	s := NewService(f, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
		close(f.release)
	}()
	_, _, _ = s.Board(ctx)

	s.mu.Lock()
	stalled := !s.lastFail.IsZero()
	s.mu.Unlock()
	if stalled {
		t.Error("an abandoned request opened the failure backoff for everyone")
	}
}
