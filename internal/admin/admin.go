package admin

import (
	"database/sql"
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

func (h *Handler) requireAdmin(r *http.Request) (string, bool) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		return "", false
	}

	var role string
	err := h.store.DB.QueryRowContext(r.Context(), `SELECT role FROM users WHERE id = $1`, userID).Scan(&role)
	if err != nil || role != "admin" {
		return "", false
	}

	return userID, true
}

type AuctionListItem struct {
	ID                 string  `json:"id"`
	ListingID          string  `json:"listing_id"`
	SellerID           string  `json:"seller_id"`
	SellerEmail        string  `json:"seller_email"`
	Title              string  `json:"title"`
	Status             string  `json:"status"`
	Currency           string  `json:"currency"`
	StartingPriceCents int64   `json:"starting_price_cents"`
	CurrentPriceCents  *int64  `json:"current_price_cents,omitempty"`
	CurrentWinnerID    *string `json:"current_winner_id,omitempty"`
	BidCount           int     `json:"bid_count"`
	StartsAt           string  `json:"starts_at"`
	EndsAt             string  `json:"ends_at"`
	CreatedAt          string  `json:"created_at"`
}

type ListAuctionsResponse struct {
	Auctions []AuctionListItem `json:"auctions"`
	Total    int               `json:"total"`
}

func (h *Handler) ListAuctions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(r); !ok {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}

	status := r.URL.Query().Get("status")
	sellerID := r.URL.Query().Get("seller_id")

	query := `
		SELECT a.id, a.listing_id, a.status, a.currency, a.starting_price_cents,
			   a.current_price_cents, a.current_winner_id, a.bid_count,
			   a.starts_at, a.ends_at, a.created_at,
			   l.title, l.seller_id, u.email
		FROM auctions a
		JOIN listings l ON a.listing_id = l.id
		JOIN users u ON l.seller_id = u.id
		WHERE 1=1
	`

	args := []interface{}{}
	argIdx := 1

	if status != "" {
		query += ` AND a.status = $` + string(rune('0'+argIdx))
		args = append(args, status)
		argIdx++
	}

	if sellerID != "" {
		query += ` AND l.seller_id = $` + string(rune('0'+argIdx))
		args = append(args, sellerID)
		argIdx++
	}

	query += ` ORDER BY a.created_at DESC LIMIT 100`

	rows, err := h.store.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "failed to fetch auctions", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var auctions []AuctionListItem
	for rows.Next() {
		var auction AuctionListItem
		var startsAt, endsAt, createdAt time.Time
		var currentPrice sql.NullInt64
		var currentWinner sql.NullString

		err := rows.Scan(
			&auction.ID, &auction.ListingID, &auction.Status, &auction.Currency,
			&auction.StartingPriceCents, &currentPrice, &currentWinner, &auction.BidCount,
			&startsAt, &endsAt, &createdAt,
			&auction.Title, &auction.SellerID, &auction.SellerEmail,
		)
		if err != nil {
			http.Error(w, "failed to scan auction", http.StatusInternalServerError)
			return
		}

		if currentPrice.Valid {
			price := currentPrice.Int64
			auction.CurrentPriceCents = &price
		}
		if currentWinner.Valid {
			winner := currentWinner.String
			auction.CurrentWinnerID = &winner
		}

		auction.StartsAt = startsAt.Format(time.RFC3339)
		auction.EndsAt = endsAt.Format(time.RFC3339)
		auction.CreatedAt = createdAt.Format(time.RFC3339)

		auctions = append(auctions, auction)
	}

	if auctions == nil {
		auctions = []AuctionListItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListAuctionsResponse{
		Auctions: auctions,
		Total:    len(auctions),
	})
}

type UserListItem struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type ListUsersResponse struct {
	Users []UserListItem `json:"users"`
	Total int            `json:"total"`
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(r); !ok {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}

	status := r.URL.Query().Get("status")
	role := r.URL.Query().Get("role")

	query := `SELECT id, email, display_name, role, status, created_at FROM users WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if status != "" {
		query += ` AND status = $` + string(rune('0'+argIdx))
		args = append(args, status)
		argIdx++
	}

	if role != "" {
		query += ` AND role = $` + string(rune('0'+argIdx))
		args = append(args, role)
		argIdx++
	}

	query += ` ORDER BY created_at DESC LIMIT 100`

	rows, err := h.store.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "failed to fetch users", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []UserListItem
	for rows.Next() {
		var user UserListItem
		var createdAt time.Time

		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.Status, &createdAt); err != nil {
			http.Error(w, "failed to scan user", http.StatusInternalServerError)
			return
		}

		user.CreatedAt = createdAt.Format(time.RFC3339)
		users = append(users, user)
	}

	if users == nil {
		users = []UserListItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListUsersResponse{
		Users: users,
		Total: len(users),
	})
}

type UpdateUserRequest struct {
	Status *string `json:"status,omitempty"`
	Role   *string `json:"role,omitempty"`
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request, targetUserID string) {
	if _, ok := h.requireAdmin(r); !ok {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	query := `UPDATE users SET updated_at = NOW()`
	args := []interface{}{}
	argIdx := 1

	if req.Status != nil {
		query += ` , status = $` + string(rune('0'+argIdx))
		args = append(args, *req.Status)
		argIdx++
	}

	if req.Role != nil {
		query += ` , role = $` + string(rune('0'+argIdx))
		args = append(args, *req.Role)
		argIdx++
	}

	query += ` WHERE id = $` + string(rune('0'+argIdx))
	args = append(args, targetUserID)

	_, err := h.store.DB.ExecContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     targetUserID,
		"status": "updated",
	})
}

type StatsResponse struct {
	TotalUsers        int     `json:"total_users"`
	ActiveUsers       int     `json:"active_users"`
	TotalAuctions     int     `json:"total_auctions"`
	LiveAuctions      int     `json:"live_auctions"`
	EndedAuctions     int     `json:"ended_auctions"`
	TotalBids         int     `json:"total_bids"`
	TotalRevenueCents int64   `json:"total_revenue_cents"`
	AvgBidsPerAuction float64 `json:"avg_bids_per_auction"`
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(r); !ok {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}

	var stats StatsResponse

	h.store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&stats.TotalUsers)
	h.store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE status = 'active'`).Scan(&stats.ActiveUsers)
	h.store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM auctions`).Scan(&stats.TotalAuctions)
	h.store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM auctions WHERE status = 'live'`).Scan(&stats.LiveAuctions)
	h.store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM auctions WHERE status = 'ended'`).Scan(&stats.EndedAuctions)
	h.store.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM bids`).Scan(&stats.TotalBids)
	h.store.DB.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(amount_cents), 0) FROM payments WHERE status = 'completed'`).Scan(&stats.TotalRevenueCents)

	if stats.TotalAuctions > 0 {
		stats.AvgBidsPerAuction = float64(stats.TotalBids) / float64(stats.TotalAuctions)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
