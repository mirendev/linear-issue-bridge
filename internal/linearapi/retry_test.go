package linearapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// flakyServer answers the first `failures` requests with fail, then serves an
// empty issues result.
func flakyServer(t *testing.T, failures int32, fail func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= failures {
			fail(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"issues": map[string]any{"nodes": []any{}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newFastRetryClient(url string) *Client {
	c := newTestClient(url)
	c.attemptTimeout = 50 * time.Millisecond
	c.retryDelay = time.Millisecond
	return c
}

func TestReadRetriesStalledAttempt(t *testing.T) {
	srv, calls := flakyServer(t, 1, func(w http.ResponseWriter, r *http.Request) {
		// Outlast the 50ms attempt timeout without depending on the server
		// noticing the client hang up.
		time.Sleep(200 * time.Millisecond)
	})
	c := newFastRetryClient(srv.URL)

	if _, err := c.FetchIssue(context.Background(), "MIR-1"); err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("server saw %d requests, want 2", n)
	}
}

func TestReadRetryAllowsSlowAnswer(t *testing.T) {
	// Every response outlasts the first attempt's deadline. The retry must
	// still get through, or an evenly slow Linear would always fail.
	srv, calls := flakyServer(t, 0, nil)
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		inner.ServeHTTP(w, r)
	})
	c := newFastRetryClient(srv.URL)

	if _, err := c.FetchIssue(context.Background(), "MIR-1"); err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("server saw %d requests, want 2", n)
	}
}

func TestReadRetriesServerErrors(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		srv, calls := flakyServer(t, 1, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		})
		c := newFastRetryClient(srv.URL)

		if _, err := c.FetchIssue(context.Background(), "MIR-1"); err != nil {
			t.Fatalf("%d: FetchIssue: %v", code, err)
		}
		if n := calls.Load(); n != 2 {
			t.Errorf("%d: server saw %d requests, want 2", code, n)
		}
	}
}

func TestReadGivesUpAfterSecondFailure(t *testing.T) {
	srv, calls := flakyServer(t, 10, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c := newFastRetryClient(srv.URL)

	if _, err := c.FetchIssue(context.Background(), "MIR-1"); err == nil {
		t.Fatal("expected error, got nil")
	}
	if n := calls.Load(); n != readAttempts {
		t.Errorf("server saw %d requests, want %d", n, readAttempts)
	}
}

func TestReadDoesNotRetryClientErrors(t *testing.T) {
	srv, calls := flakyServer(t, 10, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	c := newFastRetryClient(srv.URL)

	if _, err := c.FetchIssue(context.Background(), "MIR-1"); err == nil {
		t.Fatal("expected error, got nil")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server saw %d requests, want 1", n)
	}
}

func TestMutationNeverRetries(t *testing.T) {
	srv, calls := flakyServer(t, 10, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	c := newFastRetryClient(srv.URL)

	if _, err := c.CreateIssue(context.Background(), "team", "title", "desc", nil); err == nil {
		t.Fatal("expected error, got nil")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server saw %d requests, want 1 (a retried create could duplicate)", n)
	}
}
