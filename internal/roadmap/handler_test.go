package roadmap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeVotes struct {
	counts map[string]int
	sawIDs []string
}

func (f *fakeVotes) Counts(_ context.Context, ids []string) map[string]int {
	f.sawIDs = ids
	out := map[string]int{}
	for _, id := range ids {
		out[id] = f.counts[id]
	}
	return out
}

func serve(t *testing.T, svc *Service, v VoteStore) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	BoardHandler(svc, v).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/roadmap", nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) Board {
	t.Helper()
	var b Board
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	return b
}

func TestBoardHandlerServesTheFinishedBoard(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	rec := serve(t, svc, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	b := decode(t, rec)
	if len(b.InProgress) == 0 || len(b.Shipped) == 0 {
		t.Errorf("board came back thin: inProgress=%d shipped=%d", len(b.InProgress), len(b.Shipped))
	}
	if b.Stale {
		t.Error("a fresh board reported stale")
	}
}

// The wire format must not carry anything the page does not render. A leak
// here is how internal Linear prose reaches the public board.
func TestBoardHandlerWireFormatIsMinimal(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	rec := serve(t, svc, nil)

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cols, ok := raw["inProgress"].([]any)
	if !ok || len(cols) == 0 {
		t.Fatal("no inProgress column in the response")
	}
	card, ok := cols[0].(map[string]any)
	if !ok {
		t.Fatal("card is not an object")
	}

	allowed := map[string]bool{
		"id": true, "title": true, "description": true, "statusName": true,
		"labels": true, "release": true, "docsUrl": true, "blogUrl": true, "votes": true,
	}
	for k := range card {
		if !allowed[k] {
			t.Errorf("unexpected field %q on the wire", k)
		}
	}
}

func TestBoardHandlerMergesVotes(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)

	// Warm the cache so we can name a real id.
	warm, _, err := svc.Board(context.Background())
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	id := warm.InProgress[0].ID

	v := &fakeVotes{counts: map[string]int{id: 99}}
	b := decode(t, serve(t, svc, v))

	var got int
	for _, c := range b.InProgress {
		if c.ID == id {
			got = c.Votes
		}
	}
	if got != 99 {
		t.Errorf("votes = %d, want 99", got)
	}
	// Shipped cards are not votable but still display their count.
	if len(v.sawIDs) != len(allCardIDs(warm)) {
		t.Errorf("asked for %d counts, want %d", len(v.sawIDs), len(allCardIDs(warm)))
	}
}

// Serving a cached board must not let one request's votes leak into the next.
func TestBoardHandlerDoesNotPoisonTheCache(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	warm, _, err := svc.Board(context.Background())
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	id := warm.InProgress[0].ID

	_ = serve(t, svc, &fakeVotes{counts: map[string]int{id: 99}})
	b := decode(t, serve(t, svc, &fakeVotes{counts: map[string]int{}}))

	for _, c := range b.InProgress {
		if c.ID == id && c.Votes != 0 {
			t.Errorf("stale vote count %d survived into a later request", c.Votes)
		}
	}
}

func TestBoardHandlerFailsHonestlyWithNothingToServe(t *testing.T) {
	svc := NewService(&fakeFetcher{err: errors.New("linear down")}, time.Minute)
	rec := serve(t, svc, nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 so the caller can show its own fallback", rec.Code)
	}
}

func TestBoardHandlerFlagsStale(t *testing.T) {
	f := &fakeFetcher{}
	svc := NewService(f, time.Millisecond)
	if _, _, err := svc.Board(context.Background()); err != nil {
		t.Fatalf("warm: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	f.err = errors.New("linear down")

	if b := decode(t, serve(t, svc, nil)); !b.Stale {
		t.Error("a board served past a failed refresh did not report stale")
	}
}

// Every array on the wire must serialise as an array, never null. Go marshals
// a nil slice as null, and a consumer doing labels.map() on that gets a
// TypeError rather than an empty list — which is a 500 on the page, not a
// degraded card. Caught in a local smoke test against real Linear, where most
// cards happen to carry no display labels.
func TestBoardHandlerNeverEmitsNullArrays(t *testing.T) {
	svc := NewService(&fakeFetcher{}, time.Minute)
	rec := serve(t, svc, nil)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, col := range []string{"exploring", "upNext", "inProgress", "shipped"} {
		if string(raw[col]) == "null" {
			t.Errorf("column %q serialised as null, want []", col)
		}
		var cards []map[string]json.RawMessage
		if err := json.Unmarshal(raw[col], &cards); err != nil {
			t.Fatalf("decode %s: %v", col, err)
		}
		for i, c := range cards {
			if string(c["labels"]) == "null" {
				t.Errorf("%s[%d].labels serialised as null, want []", col, i)
			}
		}
	}
}

// The regression CodeRabbit caught: WithVotes was the only thing turning nil
// columns into empty slices, and it runs only when a vote store is configured.
// A bridge with no REDIS_URL therefore served empty columns as null.
func TestBoardHandlerNormalizesColumnsWithoutAVoteStore(t *testing.T) {
	// A fetcher with no issues leaves the issue-fed columns empty.
	svc := NewService(&emptyFetcher{}, time.Minute)
	rec := serve(t, svc, nil) // nil vote store: WithVotes never runs

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, rec.Body.String())
	}
	for _, col := range []string{"exploring", "upNext", "inProgress", "shipped"} {
		if string(raw[col]) == "null" {
			t.Errorf("column %q serialised as null with no vote store, want []", col)
		}
	}
}
