package watchlist

import (
	"encoding/json"
	"net/http"
	"time"

	"bidding/internal/auth"
	"bidding/internal/db"
)

type Handler struct {
	store *db.Store
}

func NewHandler(store *db.Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) AddToWatchlist(w http.ResponseWriter, r *http.Request, auctionID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	_, err := h.store.DB.ExecContext(r.Context(),
		`INSERT INTO watchlists (user_id, auction_id) 
		 VALUES ($1, $2)
		 ON CONFLICT (user_id, auction_id) DO NOTHING`,
		userID, auctionID,
	)
	if err != nil {
		http.Error(w, "failed to add to watchlist", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) RemoveFromWatchlist(w http.ResponseWriter, r *http.Request, auctionID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	_, err := h.store.DB.ExecContext(r.Context(),
		`DELETE FROM watchlists WHERE user_id = $1 AND auction_id = $2`,
		userID, auctionID,
	)
	if err != nil {
		http.Error(w, "failed to remove from watchlist", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type AuctionResponse struct {
	ID                  string  `json:"id"`
	ListingID           string  `json:"listing_id"`
	Status              string  `json:"status"`
	Currency            string  `json:"currency"`
	StartingPriceCents  int64   `json:"starting_price_cents"`
	CurrentPriceCents   *int64  `json:"current_price_cents,omitempty"`
	MinIncrementCents   int64   `json:"min_increment_cents"`
	StartsAt            string  `json:"starts_at"`
	EndsAt              string  `json:"ends_at"`
	BidCount            int     `json:"bid_count"`
}

type GetWatchlistResponse struct {
	Auctions []AuctionResponse `json:"auctions"`
}

func (h *Handler) GetWatchlist(w http.ResponseWriter, r *http.Request) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := h.store.DB.QueryContext(r.Context(),
		`SELECT a.id, a.listing_id, a.status, a.currency, a.starting_price_cents, 
			a.current_price_cents, a.min_increment_cents, a.starts_at, a.ends_at, a.bid_count
		 FROM auctions a
		 INNER JOIN watchlists w ON a.id = w.auction_id
		 WHERE w.user_id = $1
		 ORDER BY a.ends_at ASC
		 LIMIT 50`,
		userID,
	)
	if err != nil {
		http.Error(w, "failed to fetch watchlist", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var auctions []AuctionResponse
	for rows.Next() {
		var auction AuctionResponse
		var startsAt, endsAt time.Time
		var currentPriceCents *int64

		if err := rows.Scan(&auction.ID, &auction.ListingID, &auction.Status, &auction.Currency,
			&auction.StartingPriceCents, &currentPriceCents, &auction.MinIncrementCents,
			&startsAt, &endsAt, &auction.BidCount); err != nil {
			http.Error(w, "failed to scan auction", http.StatusInternalServerError)
			return
		}

		auction.StartsAt = startsAt.Format(time.RFC3339)
		auction.EndsAt = endsAt.Format(time.RFC3339)
		auction.CurrentPriceCents = currentPriceCents

		auctions = append(auctions, auction)
	}

	if auctions == nil {
		auctions = []AuctionResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetWatchlistResponse{Auctions: auctions})
}
