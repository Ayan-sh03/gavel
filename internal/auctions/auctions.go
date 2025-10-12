package auctions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"bidding/internal/audit"
	"bidding/internal/auth"
	"bidding/internal/cache"
	"bidding/internal/db"

	"github.com/google/uuid"
)

type Handler struct {
	store *db.Store
}

func NewHandler(store *db.Store) *Handler {
	return &Handler{store: store}
}

type CreateAuctionRequest struct {
	ListingID          string  `json:"listing_id"`
	Currency           string  `json:"currency"`
	StartingPriceCents int64   `json:"starting_price_cents"`
	ReservePriceCents  *int64  `json:"reserve_price_cents,omitempty"`
	MinIncrementCents  int64   `json:"min_increment_cents"`
	StartsAt           string  `json:"starts_at"`
	EndsAt             string  `json:"ends_at"`
	ExtensionWindowSecs int    `json:"extension_window_secs,omitempty"`
}

type AuctionResponse struct {
	ID                  string  `json:"id"`
	ListingID           string  `json:"listing_id"`
	Status              string  `json:"status"`
	Currency            string  `json:"currency"`
	StartingPriceCents  int64   `json:"starting_price_cents"`
	ReservePriceCents   *int64  `json:"reserve_price_cents,omitempty"`
	MinIncrementCents   int64   `json:"min_increment_cents"`
	StartsAt            string  `json:"starts_at"`
	EndsAt              string  `json:"ends_at"`
	ExtensionWindowSecs int     `json:"extension_window_secs"`
	CurrentPriceCents   *int64  `json:"current_price_cents,omitempty"`
	CurrentWinnerID     *string `json:"current_winner_id,omitempty"`
	BidCount            int     `json:"bid_count"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req CreateAuctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.ListingID == "" || req.Currency == "" || req.StartingPriceCents <= 0 || req.MinIncrementCents <= 0 {
		http.Error(w, "listing_id, currency, starting_price_cents, and min_increment_cents are required", http.StatusBadRequest)
		return
	}

	var sellerID string
	err := h.store.DB.QueryRowContext(r.Context(), `SELECT seller_id FROM listings WHERE id = $1`, req.ListingID).Scan(&sellerID)
	if err != nil {
		http.Error(w, "listing not found", http.StatusNotFound)
		return
	}

	if sellerID != userID {
		http.Error(w, "only the seller can create an auction for this listing", http.StatusForbidden)
		return
	}

	startsAt, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		http.Error(w, "invalid starts_at format", http.StatusBadRequest)
		return
	}

	endsAt, err := time.Parse(time.RFC3339, req.EndsAt)
	if err != nil {
		http.Error(w, "invalid ends_at format", http.StatusBadRequest)
		return
	}

	if endsAt.Before(startsAt) {
		http.Error(w, "ends_at must be after starts_at", http.StatusBadRequest)
		return
	}

	extensionWindow := req.ExtensionWindowSecs
	if extensionWindow == 0 {
		extensionWindow = 180
	}

	status := "scheduled"
	if time.Now().UTC().After(startsAt) {
		status = "live"
	}

	auctionID := uuid.New().String()
	_, err = h.store.DB.ExecContext(r.Context(),
		`INSERT INTO auctions (id, listing_id, status, currency, starting_price_cents, reserve_price_cents, 
			min_increment_cents, starts_at, ends_at, extension_window_secs, bid_count, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())`,
		auctionID, req.ListingID, status, req.Currency, req.StartingPriceCents, req.ReservePriceCents,
		req.MinIncrementCents, startsAt, endsAt, extensionWindow, 0,
	)
	if err != nil {
		http.Error(w, "failed to create auction", http.StatusInternalServerError)
		return
	}

	auction := AuctionResponse{
		ID:                  auctionID,
		ListingID:           req.ListingID,
		Status:              status,
		Currency:            req.Currency,
		StartingPriceCents:  req.StartingPriceCents,
		ReservePriceCents:   req.ReservePriceCents,
		MinIncrementCents:   req.MinIncrementCents,
		StartsAt:            startsAt.Format(time.RFC3339),
		EndsAt:              endsAt.Format(time.RFC3339),
		ExtensionWindowSecs: extensionWindow,
		BidCount:            0,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(auction)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request, auctionID string) {
	// Try cache first
	var auction AuctionResponse
	err := cache.GetCachedAuction(r.Context(), auctionID, &auction)
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		json.NewEncoder(w).Encode(auction)
		return
	}

	var startsAt, endsAt time.Time
	var currentPriceCents, reservePriceCents *int64
	var currentWinnerID *string

	err = h.store.DB.QueryRowContext(r.Context(),
		`SELECT id, listing_id, status, currency, starting_price_cents, reserve_price_cents,
			min_increment_cents, starts_at, ends_at, extension_window_secs, current_price_cents,
			current_winner_id, bid_count
		 FROM auctions WHERE id = $1`,
		auctionID,
	).Scan(&auction.ID, &auction.ListingID, &auction.Status, &auction.Currency, &auction.StartingPriceCents,
		&reservePriceCents, &auction.MinIncrementCents, &startsAt, &endsAt, &auction.ExtensionWindowSecs,
		&currentPriceCents, &currentWinnerID, &auction.BidCount)

	if err != nil {
		http.Error(w, "auction not found", http.StatusNotFound)
		return
	}

	auction.StartsAt = startsAt.Format(time.RFC3339)
	auction.EndsAt = endsAt.Format(time.RFC3339)
	auction.ReservePriceCents = reservePriceCents
	auction.CurrentPriceCents = currentPriceCents
	auction.CurrentWinnerID = currentWinnerID

	// Cache for 5 seconds
	cache.CacheAuction(r.Context(), auctionID, auction)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	json.NewEncoder(w).Encode(auction)
}

type ListAuctionsResponse struct {
	Auctions []AuctionResponse `json:"auctions"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	status := query.Get("status")

	sql := `SELECT id, listing_id, status, currency, starting_price_cents, reserve_price_cents,
		min_increment_cents, starts_at, ends_at, extension_window_secs, current_price_cents,
		current_winner_id, bid_count
		FROM auctions WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if status != "" {
		sql += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}

	sql += " ORDER BY created_at DESC LIMIT 50"

	rows, err := h.store.DB.QueryContext(r.Context(), sql, args...)
	if err != nil {
		http.Error(w, "failed to fetch auctions", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var auctions []AuctionResponse
	for rows.Next() {
		var auction AuctionResponse
		var startsAt, endsAt time.Time
		var currentPriceCents, reservePriceCents *int64
		var currentWinnerID *string

		if err := rows.Scan(&auction.ID, &auction.ListingID, &auction.Status, &auction.Currency,
			&auction.StartingPriceCents, &reservePriceCents, &auction.MinIncrementCents,
			&startsAt, &endsAt, &auction.ExtensionWindowSecs, &currentPriceCents, &currentWinnerID,
			&auction.BidCount); err != nil {
			http.Error(w, "failed to scan auction", http.StatusInternalServerError)
			return
		}

		auction.StartsAt = startsAt.Format(time.RFC3339)
		auction.EndsAt = endsAt.Format(time.RFC3339)
		auction.ReservePriceCents = reservePriceCents
		auction.CurrentPriceCents = currentPriceCents
		auction.CurrentWinnerID = currentWinnerID

		auctions = append(auctions, auction)
	}

	if auctions == nil {
		auctions = []AuctionResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListAuctionsResponse{Auctions: auctions})
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request, auctionID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var auction struct {
		SellerID string
		BidCount int
	}

	err := h.store.DB.QueryRowContext(r.Context(),
		`SELECT (SELECT seller_id FROM listings WHERE id = auctions.listing_id) as seller_id, bid_count
		 FROM auctions WHERE id = $1`,
		auctionID,
	).Scan(&auction.SellerID, &auction.BidCount)

	if err != nil {
		http.Error(w, "auction not found", http.StatusNotFound)
		return
	}

	if auction.SellerID != userID {
		http.Error(w, "only the seller can cancel this auction", http.StatusForbidden)
		return
	}

	if auction.BidCount > 0 {
		http.Error(w, "cannot cancel auction with bids", http.StatusForbidden)
		return
	}

	_, err = h.store.DB.ExecContext(r.Context(),
		`UPDATE auctions SET status = 'canceled', updated_at = NOW() WHERE id = $1`,
		auctionID,
	)
	if err != nil {
		http.Error(w, "failed to cancel auction", http.StatusInternalServerError)
		return
	}

	// Invalidate cache
	cache.InvalidateAuction(r.Context(), auctionID)

	// Audit log
	go audit.LogAuctionCanceled(r.Context(), h.store, userID, auctionID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     auctionID,
		"status": "canceled",
	})
}

type UpdateAuctionRequest struct {
	Status *string `json:"status,omitempty"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request, auctionID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var userRole string
	err := h.store.DB.QueryRowContext(r.Context(), `SELECT role FROM users WHERE id = $1`, userID).Scan(&userRole)
	if err != nil || userRole != "admin" {
		http.Error(w, "only admins can update auctions", http.StatusForbidden)
		return
	}

	var req UpdateAuctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	query := `UPDATE auctions SET updated_at = NOW()`
	args := []interface{}{}
	argIdx := 1

	if req.Status != nil {
		query += fmt.Sprintf(", status = $%d", argIdx)
		args = append(args, *req.Status)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, auctionID)

	_, err = h.store.DB.ExecContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "failed to update auction", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     auctionID,
		"status": "updated",
	})
}
