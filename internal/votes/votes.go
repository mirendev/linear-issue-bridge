// Package votes stores the roadmap's anonymous heart counts, one integer per
// Linear id, plus the fixed-window rate limiter that guards writing them.
//
// Every operation degrades gracefully. With no store configured or an
// unreachable one, reads report zero and writes report failure: the board
// still renders and voting is simply inert. Counts are deliberately soft —
// there is no per-user identity here, the browser tracks what it has hearted.
package votes

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"
)

const (
	votePrefix      = "roadmap:votes:"
	rateLimitPrefix = "roadmap:rl:"
)

// ErrUnavailable reports that the vote store could not be reached.
var ErrUnavailable = errors.New("vote store unavailable")

// backend is the narrow slice of Redis this package needs. Keeping it in plain
// Go types keeps the driver at the edge and the logic testable.
type backend interface {
	MGet(ctx context.Context, keys []string) ([]any, error)
	Incr(ctx context.Context, key string) (int64, error)
	Decr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Close() error
}

// Store reads and writes vote counts. A Store with no backend is valid and
// inert, which is what makes "runs fine without Redis" true rather than aspirational.
type Store struct {
	be backend
}

// New returns a Store backed by be. A nil backend yields an inert Store.
func New(be backend) *Store {
	return &Store{be: be}
}

// Enabled reports whether a backend is configured.
func (s *Store) Enabled() bool { return s != nil && s.be != nil }

func (s *Store) Close() error {
	if !s.Enabled() {
		return nil
	}
	return s.be.Close()
}

func keyFor(id string) string { return votePrefix + id }

// Counts returns the current count for each id, defaulting to zero. An
// unreachable store logs and reports zeros rather than failing the render.
func (s *Store) Counts(ctx context.Context, ids []string) map[string]int {
	counts := make(map[string]int, len(ids))
	for _, id := range ids {
		counts[id] = 0
	}
	if !s.Enabled() || len(ids) == 0 {
		return counts
	}

	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, keyFor(id))
	}

	values, err := s.be.MGet(ctx, keys)
	if err != nil {
		slog.Error("vote counts failed", "error", err)
		return counts
	}

	for i, v := range values {
		if i >= len(ids) {
			break
		}
		if n, ok := parseCount(v); ok && n > 0 {
			counts[ids[i]] = n
		}
	}
	return counts
}

func parseCount(v any) (int, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case string:
		n, err := strconv.Atoi(t)
		return n, err == nil
	case int64:
		return int(t), true
	case int:
		return t, true
	default:
		return 0, false
	}
}

// Direction is which way a heart moves. It is an alias rather than a defined
// type so the roadmap package can describe this store's shape in an interface
// without importing it, keeping the policy layer free of a storage dependency.
type Direction = string

const (
	Up   Direction = "up"
	Down Direction = "down"
)

// Apply moves a card's count and returns the new value.
//
// Vote keys are deliberately permanent, unlike the rate-limit keys next door.
// A count is the whole point of storing anything here, so it must outlive any
// window we would pick. The cost is that keys accumulate: one small integer per
// card that has ever been hearted, never reclaimed even after the card leaves
// the board. At roadmap scale that is a rounding error, but it is growth
// without a ceiling, so it belongs on the list if this store ever holds
// anything bigger.
//
// A down-vote clamps in the response rather than with a follow-up write: a
// second write races with concurrent votes. The stored value can therefore
// drift below zero after repeated down-votes, which is acceptable for a soft
// counter the UI only lets a browser decrement after it has incremented.
func (s *Store) Apply(ctx context.Context, id string, dir Direction) (int, error) {
	if !s.Enabled() {
		return 0, ErrUnavailable
	}
	key := keyFor(id)

	if dir == Up {
		n, err := s.be.Incr(ctx, key)
		if err != nil {
			slog.Error("apply vote failed", "id", id, "direction", dir, "error", err)
			return 0, ErrUnavailable
		}
		return int(n), nil
	}

	n, err := s.be.Decr(ctx, key)
	if err != nil {
		slog.Error("apply vote failed", "id", id, "direction", dir, "error", err)
		return 0, ErrUnavailable
	}
	return max(0, int(n)), nil
}

// WithinRateLimit reports whether key is within limit requests for the current
// window of bucket.
//
// Fail-open by design: with the store absent or erroring, requests are allowed.
// Voting is inert without a store anyway, and a limiter that failed closed
// would turn a Redis blip into a site-wide outage of the feature it guards.
//
// INCR-then-EXPIRE anchors the window to the first request in it; an unexpired
// counter simply keeps counting. Coarse, but race-tolerant and one round trip
// in the common case.
func (s *Store) WithinRateLimit(ctx context.Context, bucket, key string, limit int, window time.Duration) bool {
	if !s.Enabled() {
		return true
	}
	k := rateLimitPrefix + bucket + ":" + key
	n, err := s.be.Incr(ctx, k)
	if err != nil {
		slog.Error("rate limit check failed", "bucket", bucket, "error", err)
		return true
	}
	if n == 1 {
		if err := s.be.Expire(ctx, k, window); err != nil {
			// Without a TTL this counter never resets, so the caller would be
			// limited forever once it crosses the threshold. Drop the key and
			// let the next request start a fresh window: a missed window beats
			// a permanent lockout for a limiter that is fail-open anyway.
			slog.Error("rate limit expire failed, dropping counter", "bucket", bucket, "error", err)
			if err := s.be.Del(ctx, k); err != nil {
				slog.Error("rate limit cleanup failed", "bucket", bucket, "error", err)
			}
			return true
		}
	}
	return int(n) <= limit
}
