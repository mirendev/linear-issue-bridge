package roadmap

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// fetchTimeout deliberately exceeds the Linear client's own 10s HTTP timeout,
// so a slow upstream fails with a real error rather than having the request
// context cancel out from under it. Same reasoning as the issues endpoint.
const fetchTimeout = 15 * time.Second

// VoteStore is the slice of the vote store the handlers need.
type VoteStore interface {
	Counts(ctx context.Context, ids []string) map[string]int
}

// BoardHandler serves the finished public roadmap as JSON.
//
// The board is cached and the counts are not: the Linear-derived shape changes
// when someone relabels a project, while a heart lands whenever a visitor
// clicks. Merging per request keeps counts live without refetching Linear.
func BoardHandler(svc *Service, votes VoteStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), fetchTimeout)
		defer cancel()

		board, stale, err := svc.Board(ctx)
		if err != nil {
			slog.Error("serve roadmap", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		board.Stale = stale

		if votes != nil {
			board = WithVotes(board, votes.Counts(ctx, allCardIDs(board)))
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// The board is public and cheap to re-fetch; a short shared cache keeps
		// a burst off the bridge without hiding a relabel for long.
		w.Header().Set("Cache-Control", "public, max-age=30")
		if err := json.NewEncoder(w).Encode(board); err != nil {
			slog.Error("encode roadmap", "error", err)
		}
	}
}

func allCardIDs(b Board) []string {
	var ids []string
	for _, col := range [][]Card{b.Exploring, b.UpNext, b.InProgress, b.Shipped} {
		for _, c := range col {
			ids = append(ids, c.ID)
		}
	}
	return ids
}
