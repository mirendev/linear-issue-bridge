package roadmap

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

const (
	// DefaultTTL matches the issue cache: long enough to stay clear of
	// Linear's rate limits, short enough that a Thursday labelling pass shows
	// up without a deploy.
	DefaultTTL = 5 * time.Minute
	// FailureBackoff keeps a Linear outage from turning every render into its
	// own doomed request, each paying the full fetch timeout.
	FailureBackoff = 30 * time.Second
)

// Fetcher is the slice of the Linear client this service needs.
type Fetcher interface {
	FetchRoadmapProjects(ctx context.Context, labelNames []string) ([]*linearapi.Project, error)
	FetchRoadmapIssues(ctx context.Context, labelNames []string) ([]*linearapi.Issue, error)
}

// Service owns fetching and caching the Linear-derived board.
//
// It deliberately does not know about votes. The board is expensive and
// changes on a Thursday; counts are cheap and change on every heart. Caching
// them together would either stale the counts or throw away the board.
type Service struct {
	fetcher Fetcher
	ttl     time.Duration

	mu        sync.Mutex
	board     *Board
	fetchedAt time.Time
	lastFail  time.Time
	// inflight is non-nil while a fetch is running; it closes when that fetch
	// finishes, so concurrent callers wait for it instead of starting their own.
	inflight chan struct{}
}

func NewService(f Fetcher, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Service{fetcher: f, ttl: ttl}
}

// Board returns the current board. The bool reports whether it is stale, which
// happens when a refresh failed and a previously good board is standing in.
// It returns an error only when there is nothing at all to serve.
func (s *Service) Board(ctx context.Context) (Board, bool, error) {
	s.mu.Lock()

	if s.board != nil && time.Since(s.fetchedAt) < s.ttl {
		b := *s.board
		s.mu.Unlock()
		return b, false, nil
	}

	// A recent failure serves the last good board rather than retrying into
	// an outage on every request.
	if time.Since(s.lastFail) < FailureBackoff {
		if s.board != nil {
			b := *s.board
			s.mu.Unlock()
			return b, true, nil
		}
		s.mu.Unlock()
		return Board{}, false, errNoBoard
	}

	// Another goroutine is already fetching: wait for it rather than
	// duplicating the Linear query.
	if ch := s.inflight; ch != nil {
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return Board{}, false, ctx.Err()
		}
		return s.current()
	}

	ch := make(chan struct{})
	s.inflight = ch
	s.mu.Unlock()

	board, err := s.fetch(ctx)

	s.mu.Lock()
	s.inflight = nil
	if err != nil {
		// Only a real upstream failure opens the backoff. If this caller's own
		// context is done, the request was abandoned client-side and stalling
		// every other reader for 30s over it would be a self-inflicted outage.
		if ctx.Err() == nil {
			s.lastFail = time.Now()
		}
	} else {
		s.board = &board
		s.fetchedAt = time.Now()
		s.lastFail = time.Time{}
	}
	s.mu.Unlock()
	close(ch)

	if err != nil {
		slog.Warn("roadmap fetch failed", "error", err)
		return s.current()
	}
	return board, false, nil
}

// current reports whatever is cached, marking it stale when it is past TTL.
func (s *Service) current() (Board, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.board == nil {
		return Board{}, false, errNoBoard
	}
	return *s.board, time.Since(s.fetchedAt) >= s.ttl, nil
}

func (s *Service) fetch(ctx context.Context) (Board, error) {
	projects, err := s.fetcher.FetchRoadmapProjects(ctx, ProjectLabels)
	if err != nil {
		return Board{}, err
	}
	issues, err := s.fetcher.FetchRoadmapIssues(ctx, IssueLabels)
	if err != nil {
		return Board{}, err
	}
	return Build(projects, issues, time.Now()), nil
}

type noBoardError struct{}

func (noBoardError) Error() string { return "no roadmap board available" }

var errNoBoard = noBoardError{}

// WithVotes returns a copy of b with counts merged onto every card.
func WithVotes(b Board, counts map[string]int) Board {
	apply := func(cards []Card) []Card {
		out := make([]Card, len(cards))
		for i, c := range cards {
			c.Votes = counts[c.ID]
			out[i] = c
		}
		return out
	}
	b.Exploring = apply(b.Exploring)
	b.UpNext = apply(b.UpNext)
	b.InProgress = apply(b.InProgress)
	b.Shipped = apply(b.Shipped)
	return b
}

// OpenIDs lists the cards that may be voted on: everything except Shipped.
func OpenIDs(b Board) []string {
	var ids []string
	for _, col := range [][]Card{b.Exploring, b.UpNext, b.InProgress} {
		for _, c := range col {
			ids = append(ids, c.ID)
		}
	}
	return ids
}
