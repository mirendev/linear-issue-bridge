package cache

import (
	"context"
	"sync"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

const DefaultTTL = 5 * time.Minute

type entry struct {
	issue     *linearapi.Issue
	fetchedAt time.Time
}

type listEntry struct {
	issues    []*linearapi.Issue
	fetchedAt time.Time
}

type IssueFetcher interface {
	FetchIssue(ctx context.Context, identifier string) (*linearapi.Issue, error)
	FetchPublicIssues(ctx context.Context, teamKey string) ([]*linearapi.Issue, error)
}

type Cache struct {
	fetcher IssueFetcher
	ttl     time.Duration

	mu      sync.RWMutex
	entries map[string]*entry

	listMu      sync.RWMutex
	listEntries map[string]*listEntry
}

func New(fetcher IssueFetcher, ttl time.Duration) *Cache {
	return &Cache{
		fetcher:     fetcher,
		ttl:         ttl,
		entries:     make(map[string]*entry),
		listEntries: make(map[string]*listEntry),
	}
}

func (c *Cache) Get(ctx context.Context, identifier string) (*linearapi.Issue, error) {
	c.mu.RLock()
	e, ok := c.entries[identifier]
	c.mu.RUnlock()

	if ok && time.Since(e.fetchedAt) < c.ttl {
		return e.issue, nil
	}

	issue, err := c.fetcher.FetchIssue(ctx, identifier)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[identifier] = &entry{
		issue:     issue,
		fetchedAt: time.Now(),
	}
	c.mu.Unlock()

	return issue, nil
}

func (c *Cache) GetPublicIssues(ctx context.Context, teamKey string) ([]*linearapi.Issue, error) {
	c.listMu.RLock()
	e, ok := c.listEntries[teamKey]
	c.listMu.RUnlock()

	if ok && time.Since(e.fetchedAt) < c.ttl {
		return e.issues, nil
	}

	issues, err := c.fetcher.FetchPublicIssues(ctx, teamKey)
	if err != nil {
		return nil, err
	}

	c.listMu.Lock()
	c.listEntries[teamKey] = &listEntry{
		issues:    issues,
		fetchedAt: time.Now(),
	}
	c.listMu.Unlock()

	return issues, nil
}
