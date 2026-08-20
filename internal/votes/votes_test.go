package votes

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

type fakeBackend struct {
	values    map[string]int64
	expires   map[string]time.Duration
	fail      bool
	incrs     int
	deleted   []string
	expireErr bool
}

func newFake() *fakeBackend {
	return &fakeBackend{values: map[string]int64{}, expires: map[string]time.Duration{}}
}

var errBoom = errors.New("boom")

func (f *fakeBackend) MGet(_ context.Context, keys []string) ([]any, error) {
	if f.fail {
		return nil, errBoom
	}
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		v, ok := f.values[k]
		if !ok {
			out = append(out, nil)
			continue
		}
		out = append(out, strconv.FormatInt(v, 10))
	}
	return out, nil
}

func (f *fakeBackend) Incr(_ context.Context, key string) (int64, error) {
	f.incrs++
	if f.fail {
		return 0, errBoom
	}
	f.values[key]++
	return f.values[key], nil
}

func (f *fakeBackend) Decr(_ context.Context, key string) (int64, error) {
	if f.fail {
		return 0, errBoom
	}
	f.values[key]--
	return f.values[key], nil
}

func (f *fakeBackend) Expire(_ context.Context, key string, ttl time.Duration) error {
	if f.fail || f.expireErr {
		return errBoom
	}
	f.expires[key] = ttl
	return nil
}

func (f *fakeBackend) Del(_ context.Context, key string) error {
	if f.fail {
		return errBoom
	}
	delete(f.values, key)
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeBackend) Close() error { return nil }

func TestCountsDefaultToZero(t *testing.T) {
	f := newFake()
	f.values[keyFor("a")] = 7
	s := New(f)

	got := s.Counts(context.Background(), []string{"a", "b"})
	if got["a"] != 7 {
		t.Errorf("counts[a] = %d, want 7", got["a"])
	}
	if got["b"] != 0 {
		t.Errorf("counts[b] = %d, want 0", got["b"])
	}
}

// A negative stored value is drift from clamped down-votes; it must read as 0.
func TestCountsClampNegativeDrift(t *testing.T) {
	f := newFake()
	f.values[keyFor("a")] = -3
	if got := New(f).Counts(context.Background(), []string{"a"}); got["a"] != 0 {
		t.Errorf("counts[a] = %d, want 0", got["a"])
	}
}

// The board must render even when the store is down.
func TestCountsDegradeToZeroOnError(t *testing.T) {
	f := newFake()
	f.fail = true
	got := New(f).Counts(context.Background(), []string{"a", "b"})
	if got["a"] != 0 || got["b"] != 0 || len(got) != 2 {
		t.Errorf("counts = %v, want both zero", got)
	}
}

// No store configured at all is a supported mode, not an error.
func TestInertStore(t *testing.T) {
	s := New(nil)
	if s.Enabled() {
		t.Error("a store with no backend reports enabled")
	}
	if got := s.Counts(context.Background(), []string{"a"}); got["a"] != 0 {
		t.Errorf("counts[a] = %d, want 0", got["a"])
	}
	if _, err := s.Apply(context.Background(), "a", Up); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Apply err = %v, want ErrUnavailable", err)
	}
	if !s.WithinRateLimit(context.Background(), "vote", "ip", 1, time.Minute) {
		t.Error("rate limit must fail open with no store")
	}
}

func TestApplyUpAndDown(t *testing.T) {
	f := newFake()
	s := New(f)
	ctx := context.Background()

	if n, err := s.Apply(ctx, "a", Up); err != nil || n != 1 {
		t.Fatalf("Apply(up) = %d, %v", n, err)
	}
	if n, err := s.Apply(ctx, "a", Up); err != nil || n != 2 {
		t.Fatalf("Apply(up) = %d, %v", n, err)
	}
	if n, err := s.Apply(ctx, "a", Down); err != nil || n != 1 {
		t.Fatalf("Apply(down) = %d, %v", n, err)
	}
}

// Down-votes clamp in the response only. A follow-up write would race with
// concurrent votes, which is the TOCTOU the website hit.
func TestApplyDownClampsInResponseNotStorage(t *testing.T) {
	f := newFake()
	s := New(f)

	n, err := s.Apply(context.Background(), "a", Down)
	if err != nil {
		t.Fatalf("Apply(down) err = %v", err)
	}
	if n != 0 {
		t.Errorf("Apply(down) = %d, want 0", n)
	}
	if got := f.values[keyFor("a")]; got != -1 {
		t.Errorf("stored value = %d, want -1 (clamping must not write back)", got)
	}
}

func TestApplyReportsUnavailable(t *testing.T) {
	f := newFake()
	f.fail = true
	if _, err := New(f).Apply(context.Background(), "a", Up); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Apply err = %v, want ErrUnavailable", err)
	}
}

func TestRateLimitBoundary(t *testing.T) {
	f := newFake()
	s := New(f)
	ctx := context.Background()

	if !s.WithinRateLimit(ctx, "vote", "ip", 2, time.Minute) {
		t.Error("first request must be allowed")
	}
	if !s.WithinRateLimit(ctx, "vote", "ip", 2, time.Minute) {
		t.Error("second request must be allowed at limit 2")
	}
	if s.WithinRateLimit(ctx, "vote", "ip", 2, time.Minute) {
		t.Error("third request must be refused at limit 2")
	}
}

// The window is anchored to the first request in it.
func TestRateLimitSetsTTLOnceOnly(t *testing.T) {
	f := newFake()
	s := New(f)
	ctx := context.Background()

	s.WithinRateLimit(ctx, "vote", "ip", 5, 90*time.Second)
	if got := f.expires[rateLimitPrefix+"vote:ip"]; got != 90*time.Second {
		t.Errorf("ttl = %v, want 90s", got)
	}

	delete(f.expires, rateLimitPrefix+"vote:ip")
	s.WithinRateLimit(ctx, "vote", "ip", 5, 90*time.Second)
	if _, reset := f.expires[rateLimitPrefix+"vote:ip"]; reset {
		t.Error("ttl was reset on a later request in the same window")
	}
}

func TestRateLimitFailsOpen(t *testing.T) {
	f := newFake()
	f.fail = true
	if !New(f).WithinRateLimit(context.Background(), "vote", "ip", 1, time.Minute) {
		t.Error("a failing store must fail open")
	}
}

// Separate buckets and separate callers must not share a counter.
func TestRateLimitKeysAreIsolated(t *testing.T) {
	f := newFake()
	s := New(f)
	ctx := context.Background()

	s.WithinRateLimit(ctx, "vote", "ip1", 1, time.Minute)
	if !s.WithinRateLimit(ctx, "vote", "ip2", 1, time.Minute) {
		t.Error("a second caller was refused on the first caller's budget")
	}
	if !s.WithinRateLimit(ctx, "suggest", "ip1", 1, time.Minute) {
		t.Error("a second bucket was refused on the first bucket's budget")
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"valkey://host:6379", "redis://host:6379"},
		{"valkeys://host:6379", "rediss://host:6379"},
		{"redis://host:6379", "redis://host:6379"},
		{"rediss://host:6379", "rediss://host:6379"},
	}
	for _, tt := range tests {
		if got := normalizeURL(tt.in); got != tt.want {
			t.Errorf("normalizeURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A counter with no TTL never resets, so a failed Expire must not leave one
// behind — otherwise that caller is rate-limited for the life of the store.
func TestRateLimitDropsCounterWhenExpireFails(t *testing.T) {
	f := newFake()
	f.expireErr = true
	s := New(f)

	if !s.WithinRateLimit(context.Background(), "vote", "ip", 1, time.Minute) {
		t.Error("a failed expire must not refuse the request")
	}
	key := rateLimitPrefix + "vote:ip"
	if _, still := f.values[key]; still {
		t.Error("the counter survived a failed expire and would never reset")
	}
	if len(f.deleted) != 1 || f.deleted[0] != key {
		t.Errorf("deleted = %v, want the untimed counter", f.deleted)
	}
}
