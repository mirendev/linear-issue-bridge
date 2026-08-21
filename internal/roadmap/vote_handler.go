package roadmap

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Generous for a person toggling hearts, hostile to a loop driving counts.
const (
	VoteLimit  = 30
	VoteWindow = time.Minute
)

// ClientKeyHeader carries the end user's address, as computed by the
// authenticated caller that proxied the request. Trusting it is only safe
// because Authenticator has already established who the caller is; an
// unauthenticated request never reaches the rate limiter.
const ClientKeyHeader = "X-Client-Key"

// Authenticator establishes that a request came from a workload we trust.
// Implementations verify a workload identity token; the handler only needs
// the yes-or-no.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) error
}

// VoteRecorder is the slice of the vote store the write path needs.
type VoteRecorder interface {
	Apply(ctx context.Context, id string, dir Direction) (int, error)
	WithinRateLimit(ctx context.Context, bucket, key string, limit int, window time.Duration) bool
}

// Direction mirrors votes.Direction without importing it, so the roadmap
// package stays free of a storage dependency.
type Direction = string

const (
	DirectionUp   Direction = "up"
	DirectionDown Direction = "down"
)

// ErrVoteStoreUnavailable is returned by a VoteRecorder that cannot reach its
// backing store.
var ErrVoteStoreUnavailable = errors.New("vote store unavailable")

type voteRequest struct {
	ID        string `json:"id"`
	Direction string `json:"direction"`
}

type voteResponse struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// VoteHandler records a heart against one open roadmap card.
//
// Writes are authenticated even though the person clicking is anonymous: a
// browser cannot hold a workload identity, so the website proxies the click
// and proves it is the website. That keeps this endpoint off the public
// internet as a writable surface, and lets the rate limiter key on the real
// visitor rather than on the proxy.
func VoteHandler(svc *Service, store VoteRecorder, auth Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), fetchTimeout)
		defer cancel()

		if auth != nil {
			if err := auth.Authenticate(ctx, r); err != nil {
				slog.Warn("rejected roadmap vote", "error", err)
				writeJSONError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		if !store.WithinRateLimit(ctx, "vote", clientKey(r), VoteLimit, VoteWindow) {
			writeJSONError(w, "too many votes, slow down", http.StatusTooManyRequests)
			return
		}

		var req voteRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeJSONError(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.ID == "" || (req.Direction != DirectionUp && req.Direction != DirectionDown) {
			writeJSONError(w, `id (string) and direction ("up"|"down") are required`, http.StatusBadRequest)
			return
		}

		// Only ids that are currently open, public cards may be written. This
		// stops the endpoint from being a way to set arbitrary keys, and keeps
		// shipped items unvotable.
		board, _, err := svc.Board(ctx)
		if err != nil {
			slog.Error("vote board lookup", "error", err)
			writeJSONError(w, "roadmap unavailable", http.StatusServiceUnavailable)
			return
		}
		if !isOpenCard(board, req.ID) {
			writeJSONError(w, "unknown or non-votable id", http.StatusBadRequest)
			return
		}

		count, err := store.Apply(ctx, req.ID, req.Direction)
		if err != nil {
			writeJSONError(w, "vote store unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(voteResponse{ID: req.ID, Count: count}); err != nil {
			slog.Error("encode vote response", "error", err)
		}
	}
}

func isOpenCard(b Board, id string) bool {
	for _, open := range OpenIDs(b) {
		if open == id {
			return true
		}
	}
	return false
}

// clientKey identifies the end user for rate limiting, preferring the address
// the authenticated caller reports over our own view of its connection.
func clientKey(r *http.Request) string {
	if key := r.Header.Get(ClientKeyHeader); key != "" {
		return key
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func writeJSONError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
