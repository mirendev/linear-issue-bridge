package cache

import (
	"context"
	"sync"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

const (
	DefaultTTL = 5 * time.Minute

	// DefaultMaxStale bounds how long an entry may be served while Linear
	// refuses to refresh it. Past this, a request waits on Linear like a miss.
	// It is also the longest an issue made private during an outage can keep
	// showing, so it stays short: long enough to ride out a blip, no more.
	DefaultMaxStale = 15 * time.Minute

	// fetchTimeout bounds a single background fetch. Fetches run detached from
	// the request that started them, so a reader who gives up early still
	// leaves a warm cache behind for the next one.
	fetchTimeout = 20 * time.Second
)

type IssueFetcher interface {
	FetchIssue(ctx context.Context, identifier string) (*linearapi.Issue, error)
	FetchPublicIssues(ctx context.Context, teamKey string) ([]*linearapi.Issue, error)
}

type Cache struct {
	fetcher IssueFetcher
	issues  *store[*linearapi.Issue]
	lists   *store[[]*linearapi.Issue]
}

func New(fetcher IssueFetcher, ttl time.Duration) *Cache {
	return &Cache{
		fetcher: fetcher,
		issues:  newStore[*linearapi.Issue](ttl, DefaultMaxStale),
		lists:   newStore[[]*linearapi.Issue](ttl, DefaultMaxStale),
	}
}

// Get returns the issue for identifier, or nil if Linear has no such issue.
//
// A fresh entry returns immediately. A stale one also returns immediately and
// kicks off a background refresh. Only a miss waits on Linear, and only until
// ctx is done: the fetch itself carries on, so a caller that times out can
// come back and find the result.
func (c *Cache) Get(ctx context.Context, identifier string) (*linearapi.Issue, error) {
	return c.issues.get(ctx, identifier, func(ctx context.Context) (*linearapi.Issue, error) {
		return c.fetcher.FetchIssue(ctx, identifier)
	})
}

func (c *Cache) Invalidate(identifier string) {
	c.issues.invalidate(identifier)
	c.lists.invalidateAll()
}

// GetPublicIssues follows the same stale-while-revalidate rules as Get.
func (c *Cache) GetPublicIssues(ctx context.Context, teamKey string) ([]*linearapi.Issue, error) {
	return c.lists.get(ctx, teamKey, func(ctx context.Context) ([]*linearapi.Issue, error) {
		return c.fetcher.FetchPublicIssues(ctx, teamKey)
	})
}

type entry[T any] struct {
	val       T
	fetchedAt time.Time
}

type call[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// store is a keyed stale-while-revalidate cache with one fetch in flight per
// key. Errors are never cached; a failed refresh leaves the old entry alone.
type store[T any] struct {
	ttl      time.Duration
	maxStale time.Duration

	mu       sync.Mutex
	entries  map[string]*entry[T]
	inflight map[string]*call[T]
}

func newStore[T any](ttl, maxStale time.Duration) *store[T] {
	return &store[T]{
		ttl:      ttl,
		maxStale: max(ttl, maxStale),
		entries:  make(map[string]*entry[T]),
		inflight: make(map[string]*call[T]),
	}
}

func (s *store[T]) get(ctx context.Context, key string, fetch func(context.Context) (T, error)) (T, error) {
	s.mu.Lock()
	if e, ok := s.entries[key]; ok {
		age := time.Since(e.fetchedAt)
		if age < s.ttl {
			s.mu.Unlock()
			return e.val, nil
		}
		if age < s.maxStale {
			s.startLocked(key, fetch)
			s.mu.Unlock()
			return e.val, nil
		}
	}
	c := s.startLocked(key, fetch)
	s.mu.Unlock()

	select {
	case <-c.done:
		return c.val, c.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func (s *store[T]) startLocked(key string, fetch func(context.Context) (T, error)) *call[T] {
	if c, ok := s.inflight[key]; ok {
		return c
	}
	c := &call[T]{done: make(chan struct{})}
	s.inflight[key] = c

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		c.val, c.err = fetch(ctx)

		s.mu.Lock()
		// An invalidate while we were fetching drops us from inflight; our
		// answer may predate whatever prompted it, so don't store it.
		if s.inflight[key] == c {
			delete(s.inflight, key)
			if c.err == nil {
				s.entries[key] = &entry[T]{val: c.val, fetchedAt: time.Now()}
			}
		}
		s.mu.Unlock()
		close(c.done)
	}()
	return c
}

func (s *store[T]) invalidate(key string) {
	s.mu.Lock()
	delete(s.entries, key)
	delete(s.inflight, key)
	s.mu.Unlock()
}

func (s *store[T]) invalidateAll() {
	s.mu.Lock()
	s.entries = make(map[string]*entry[T])
	s.inflight = make(map[string]*call[T])
	s.mu.Unlock()
}
