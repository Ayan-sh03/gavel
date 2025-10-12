package bids

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"bidding/internal/auth"
	"bidding/internal/db"
	"bidding/internal/realtime"

	"github.com/google/uuid"
)

type Handler struct {
	store *db.Store
}

func NewHandler(store *db.Store) *Handler {
	return &Handler{store: store}
}

type PlaceBidRequest struct {
	AmountCents int64 `json:"amount_cents"`
}

type PlaceBidResponse struct {
	Accepted          bool    `json:"accepted"`
	CurrentPriceCents int64   `json:"current_price_cents"`
	NextMinCents      *int64  `json:"next_min_cents,omitempty"`
	EndsAt            string  `json:"ends_at"`
	BidCount          int     `json:"bid_count"`
	Reason            *string `json:"reason,omitempty"`
}

func (h *Handler) PlaceBid(w http.ResponseWriter, r *http.Request, auctionID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req PlaceBidRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.AmountCents <= 0 {
		http.Error(w, "amount_cents must be positive", http.StatusBadRequest)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	tx, err := h.store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "failed to start transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if idempotencyKey != "" {
		var existingBidID string
		err := tx.QueryRowContext(r.Context(),
			`SELECT id FROM bids WHERE auction_id = $1 AND client_key = $2`,
			auctionID, idempotencyKey,
		).Scan(&existingBidID)
		if err == nil {
			var accepted bool
			var currentPrice int64
			var endsAt time.Time
			var bidCount int

			tx.QueryRowContext(r.Context(),
				`SELECT accepted, amount_cents FROM bids WHERE id = $1`,
				existingBidID,
			).Scan(&accepted, &currentPrice)

			tx.QueryRowContext(r.Context(),
				`SELECT ends_at, bid_count FROM auctions WHERE id = $1`,
				auctionID,
			).Scan(&endsAt, &bidCount)

			tx.Commit()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(PlaceBidResponse{
				Accepted:          accepted,
				CurrentPriceCents: currentPrice,
				EndsAt:            endsAt.Format(time.RFC3339),
				BidCount:          bidCount,
			})
			return
		}
	}

	var auction struct {
		Status              string
		SellerID            string
		StartingPriceCents  int64
		MinIncrementCents   int64
		EndsAt              time.Time
		ExtensionWindowSecs int
		CurrentPriceCents   sql.NullInt64
		BidCount            int
	}

	err = tx.QueryRowContext(r.Context(),
		`SELECT status, starting_price_cents, min_increment_cents, ends_at, extension_window_secs, 
			current_price_cents, bid_count,
			(SELECT seller_id FROM listings WHERE id = auctions.listing_id) as seller_id
		 FROM auctions WHERE id = $1 FOR UPDATE`,
		auctionID,
	).Scan(&auction.Status, &auction.StartingPriceCents, &auction.MinIncrementCents,
		&auction.EndsAt, &auction.ExtensionWindowSecs, &auction.CurrentPriceCents,
		&auction.BidCount, &auction.SellerID)

	if err != nil {
		http.Error(w, "auction not found", http.StatusNotFound)
		return
	}

	now := time.Now().UTC()

	if auction.Status != "live" || now.After(auction.EndsAt) {
		http.Error(w, "auction is not active", http.StatusGone)
		return
	}

	if userID == auction.SellerID {
		http.Error(w, "seller cannot bid on own auction", http.StatusForbidden)
		return
	}

	currentPrice := auction.StartingPriceCents
	if auction.CurrentPriceCents.Valid {
		currentPrice = auction.CurrentPriceCents.Int64
	}

	nextMin := currentPrice + auction.MinIncrementCents

	if req.AmountCents < nextMin {
		tx.Commit()
		reason := "bid too low"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(PlaceBidResponse{
			Accepted:          false,
			CurrentPriceCents: currentPrice,
			NextMinCents:      &nextMin,
			EndsAt:            auction.EndsAt.Format(time.RFC3339),
			BidCount:          auction.BidCount,
			Reason:            &reason,
		})
		return
	}

	newEndsAt := auction.EndsAt
	timeUntilEnd := auction.EndsAt.Sub(now).Seconds()
	if timeUntilEnd <= float64(auction.ExtensionWindowSecs) {
		newEndsAt = now.Add(time.Duration(auction.ExtensionWindowSecs) * time.Second)
		if newEndsAt.Before(auction.EndsAt) {
			newEndsAt = auction.EndsAt
		}
	}

	bidID := uuid.New().String()
	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO bids (id, auction_id, bidder_id, amount_cents, placed_at, client_key, accepted)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		bidID, auctionID, userID, req.AmountCents, now, sql.NullString{String: idempotencyKey, Valid: idempotencyKey != ""}, true,
	)
	if err != nil {
		http.Error(w, "failed to place bid", http.StatusInternalServerError)
		return
	}

	_, err = tx.ExecContext(r.Context(),
		`UPDATE auctions 
		 SET current_price_cents = $1, current_winner_id = $2, bid_count = bid_count + 1, 
		     ends_at = $3, updated_at = $4
		 WHERE id = $5`,
		req.AmountCents, userID, newEndsAt, now, auctionID,
	)
	if err != nil {
		http.Error(w, "failed to update auction", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "failed to commit transaction", http.StatusInternalServerError)
		return
	}

	// Publish SSE event for bid placed
	realtime.PublishBidPlaced(auctionID, realtime.BidPlacedData{
		BidderID:          userID,
		AmountCents:       req.AmountCents,
		CurrentPriceCents: req.AmountCents,
		BidCount:          auction.BidCount + 1,
		EndsAt:            newEndsAt.Format(time.RFC3339),
	})

	// Publish time extension event if time was extended
	if newEndsAt.After(auction.EndsAt) {
		extendedBy := int(newEndsAt.Sub(auction.EndsAt).Seconds())
		realtime.PublishTimeExtended(auctionID, realtime.TimeExtendedData{
			NewEndsAt:  newEndsAt.Format(time.RFC3339),
			ExtendedBy: extendedBy,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(PlaceBidResponse{
		Accepted:          true,
		CurrentPriceCents: req.AmountCents,
		EndsAt:            newEndsAt.Format(time.RFC3339),
		BidCount:          auction.BidCount + 1,
	})
}

type BidResponse struct {
	ID          string `json:"id"`
	BidderID    string `json:"bidder_id"`
	AmountCents int64  `json:"amount_cents"`
	PlacedAt    string `json:"placed_at"`
	Accepted    bool   `json:"accepted"`
}

type GetBidsResponse struct {
	Bids []BidResponse `json:"bids"`
}

func (h *Handler) GetBids(w http.ResponseWriter, r *http.Request, auctionID string) {
	rows, err := h.store.DB.QueryContext(r.Context(),
		`SELECT id, bidder_id, amount_cents, placed_at, accepted
		 FROM bids WHERE auction_id = $1
		 ORDER BY placed_at DESC LIMIT 50`,
		auctionID,
	)
	if err != nil {
		http.Error(w, "failed to fetch bids", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var bids []BidResponse
	for rows.Next() {
		var bid BidResponse
		var placedAt time.Time

		if err := rows.Scan(&bid.ID, &bid.BidderID, &bid.AmountCents, &placedAt, &bid.Accepted); err != nil {
			http.Error(w, "failed to scan bid", http.StatusInternalServerError)
			return
		}

		bid.PlacedAt = placedAt.Format(time.RFC3339)
		bids = append(bids, bid)
	}

	if bids == nil {
		bids = []BidResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetBidsResponse{Bids: bids})
}
