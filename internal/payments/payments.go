package payments

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"bidding/internal/auth"
	"bidding/internal/db"
)

type Handler struct {
	store *db.Store
}

func NewHandler(store *db.Store) *Handler {
	return &Handler{store: store}
}

type PayRequest struct {
	PaymentMethod string `json:"payment_method"`
}

type PaymentResponse struct {
	ID            string `json:"id"`
	AuctionID     string `json:"auction_id"`
	WinnerID      string `json:"winner_id"`
	AmountCents   int64  `json:"amount_cents"`
	Status        string `json:"status"`
	PaymentMethod string `json:"payment_method,omitempty"`
}

func (h *Handler) Pay(w http.ResponseWriter, r *http.Request, auctionID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req PayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	tx, err := h.store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "failed to start transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var auction struct {
		WinnerID    *string
		Status      string
		CurrentPrice int64
	}

	err = tx.QueryRowContext(r.Context(),
		`SELECT current_winner_id, status, COALESCE(current_price_cents, starting_price_cents)
		 FROM auctions WHERE id = $1`,
		auctionID,
	).Scan(&auction.WinnerID, &auction.Status, &auction.CurrentPrice)

	if err != nil {
		http.Error(w, "auction not found", http.StatusNotFound)
		return
	}

	if auction.Status != "ended" {
		http.Error(w, "auction has not ended yet", http.StatusBadRequest)
		return
	}

	if auction.WinnerID == nil || *auction.WinnerID != userID {
		http.Error(w, "only the winner can pay for this auction", http.StatusForbidden)
		return
	}

	var paymentID string
	err = tx.QueryRowContext(r.Context(),
		`SELECT id FROM payments WHERE auction_id = $1`,
		auctionID,
	).Scan(&paymentID)

	if err != nil && err != sql.ErrNoRows {
		http.Error(w, "failed to check existing payment", http.StatusInternalServerError)
		return
	}

	if paymentID != "" {
		var existingPayment PaymentResponse
		err = tx.QueryRowContext(r.Context(),
			`SELECT id, auction_id, winner_id, amount_cents, status FROM payments WHERE id = $1`,
			paymentID,
		).Scan(&existingPayment.ID, &existingPayment.AuctionID, &existingPayment.WinnerID, &existingPayment.AmountCents, &existingPayment.Status)

		if err != nil {
			http.Error(w, "failed to fetch payment", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(existingPayment)
		return
	}

	paymentID = "pay-" + auctionID

	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO payments (id, auction_id, winner_id, amount_cents, status, provider, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		paymentID, auctionID, userID, auction.CurrentPrice, "completed", "mock",
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create payment: %v", err), http.StatusInternalServerError)
		return
	}

	var sellerID string
	tx.QueryRowContext(r.Context(),
		`SELECT seller_id FROM listings WHERE id = (SELECT listing_id FROM auctions WHERE id = $1)`,
		auctionID,
	).Scan(&sellerID)

	var payoutExists int
	tx.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM payouts WHERE auction_id = $1`,
		auctionID,
	).Scan(&payoutExists)

	if payoutExists > 0 {
		_, err = tx.ExecContext(r.Context(),
			`UPDATE payouts SET status = 'pending' WHERE auction_id = $1`,
			auctionID,
		)
	} else {
		sellerAmount := auction.CurrentPrice * 95 / 100
		_, err = tx.ExecContext(r.Context(),
			`INSERT INTO payouts (id, auction_id, seller_id, amount_cents, status, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, NOW(), NOW())`,
			"payout-"+auctionID, auctionID, sellerID, sellerAmount, "pending",
		)
	}
	
	if err != nil {
		http.Error(w, "failed to handle payout", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "failed to commit transaction", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PaymentResponse{
		ID:            paymentID,
		AuctionID:     auctionID,
		WinnerID:      userID,
		AmountCents:   auction.CurrentPrice,
		Status:        "completed",
		PaymentMethod: req.PaymentMethod,
	})
}

func (h *Handler) GetPayment(w http.ResponseWriter, r *http.Request, paymentID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var payment PaymentResponse
	err := h.store.DB.QueryRowContext(r.Context(),
		`SELECT id, auction_id, winner_id, amount_cents, status FROM payments WHERE id = $1`,
		paymentID,
	).Scan(&payment.ID, &payment.AuctionID, &payment.WinnerID, &payment.AmountCents, &payment.Status)

	if err != nil {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}

	var userRole string
	h.store.DB.QueryRowContext(r.Context(), `SELECT role FROM users WHERE id = $1`, userID).Scan(&userRole)

	if payment.WinnerID != userID && userRole != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payment)
}
