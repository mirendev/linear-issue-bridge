package roadmap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeRecorder struct {
	counts     map[string]int
	unavail    bool
	allowRate  bool
	rateKeys   []string
	applyCalls int
}

func newRecorder() *fakeRecorder {
	return &fakeRecorder{counts: map[string]int{}, allowRate: true}
}

func (f *fakeRecorder) Apply(_ context.Context, id string, dir Direction) (int, error) {
	f.applyCalls++
	if f.unavail {
		return 0, ErrVoteStoreUnavailable
	}
	if dir == DirectionUp {
		f.counts[id]++
	} else {
		f.counts[id]--
	}
	return max(0, f.counts[id]), nil
}

func (f *fakeRecorder) WithinRateLimit(_ context.Context, _, key string, _ int, _ time.Duration) bool {
	f.rateKeys = append(f.rateKeys, key)
	return f.allowRate
}

type fakeAuth struct{ err error }

func (f fakeAuth) Authenticate(context.Context, *http.Request) error { return f.err }

func voteReq(body string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/roadmap/vote", strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func postVote(t *testing.T, svc *Service, store VoteRecorder, auth Authenticator, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	VoteHandler(svc, store, auth).ServeHTTP(rec, voteReq(body, headers))
	return rec
}

func openCardID(t *testing.T, svc *Service) string {
	t.Helper()
	b, _, err := svc.Board(context.Background())
	if err != nil {
		t.Fatalf("warm board: %v", err)
	}
	return b.InProgress[0].ID
}

func TestVoteHappyPath(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()

	rec := postVote(t, svc, store, fakeAuth{}, `{"id":"`+id+`","direction":"up"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp voteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != id || resp.Count != 1 {
		t.Errorf("response = %+v, want id=%s count=1", resp, id)
	}
}

// An unauthenticated caller must be turned away before anything is written.
func TestVoteRequiresAuthentication(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()

	rec := postVote(t, svc, store, fakeAuth{err: errors.New("bad token")},
		`{"id":"`+id+`","direction":"up"}`, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if store.applyCalls != 0 {
		t.Error("a rejected request still reached the vote store")
	}
}

// Shipped cards are display-only; accepting a write against one would let the
// endpoint set keys the board never reads back.
func TestVoteRejectsNonVotableIDs(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	b, _, err := svc.Board(context.Background())
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	store := newRecorder()

	for _, tc := range []struct{ name, id string }{
		{"shipped card", b.Shipped[0].ID},
		{"unknown id", "not-a-real-project"},
		{"denylisted id", denyID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postVote(t, svc, store, fakeAuth{}, `{"id":"`+tc.id+`","direction":"up"}`, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
	if store.applyCalls != 0 {
		t.Error("a non-votable id reached the vote store")
	}
}

func TestVoteValidatesBody(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()

	for _, tc := range []struct{ name, body string }{
		{"not json", `{`},
		{"missing id", `{"direction":"up"}`},
		{"missing direction", `{"id":"` + id + `"}`},
		{"bogus direction", `{"id":"` + id + `","direction":"sideways"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := postVote(t, svc, store, fakeAuth{}, tc.body, nil); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestVoteRateLimited(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()
	store.allowRate = false

	rec := postVote(t, svc, store, fakeAuth{}, `{"id":"`+id+`","direction":"up"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if store.applyCalls != 0 {
		t.Error("a rate-limited request still reached the vote store")
	}
}

// The limiter must key on the visitor the proxy reports, not on the proxy.
func TestVoteRateLimitsOnTheForwardedClient(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()

	postVote(t, svc, store, fakeAuth{}, `{"id":"`+id+`","direction":"up"}`,
		map[string]string{ClientKeyHeader: "203.0.113.7"})

	if len(store.rateKeys) != 1 || store.rateKeys[0] != "203.0.113.7" {
		t.Errorf("rate limit keys = %v, want the forwarded client address", store.rateKeys)
	}
}

// With no forwarded client the limiter falls back to the peer, and must not
// carry the port or every request would get its own budget.
func TestVoteRateLimitFallsBackToPeerAddress(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()

	postVote(t, svc, store, fakeAuth{}, `{"id":"`+id+`","direction":"up"}`, nil)

	if len(store.rateKeys) != 1 {
		t.Fatalf("rate limit keys = %v", store.rateKeys)
	}
	if strings.Contains(store.rateKeys[0], ":") {
		t.Errorf("rate limit key %q still carries a port", store.rateKeys[0])
	}
}

func TestVoteReportsStoreOutage(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()
	store.unavail = true

	rec := postVote(t, svc, store, fakeAuth{}, `{"id":"`+id+`","direction":"up"}`, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// A down-vote clamps at zero in the response.
func TestVoteDownClamps(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	id := openCardID(t, svc)
	store := newRecorder()

	rec := postVote(t, svc, store, fakeAuth{}, `{"id":"`+id+`","direction":"down"}`, nil)
	var resp voteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("count = %d, want 0", resp.Count)
	}
}
