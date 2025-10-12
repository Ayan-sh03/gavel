package listings

import (
	"encoding/json"
	"fmt"
	"net/http"

	"bidding/internal/auth"
	"bidding/internal/db"

	"github.com/google/uuid"
)

type Handler struct {
	store *db.Store
}

func NewHandler(store *db.Store) *Handler {
	return &Handler{store: store}
}

type CreateListingRequest struct {
	Title          string `json:"title"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	Condition      string `json:"condition"`
	CoverImageURL  string `json:"cover_image_url"`
}

type ListingResponse struct {
	ID             string `json:"id"`
	SellerID       string `json:"seller_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	Condition      string `json:"condition"`
	CoverImageURL  string `json:"cover_image_url"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req CreateListingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.Title == "" || req.Description == "" || req.Category == "" || req.Condition == "" {
		http.Error(w, "title, description, category, and condition are required", http.StatusBadRequest)
		return
	}

	listingID := uuid.New().String()
	_, err := h.store.DB.ExecContext(r.Context(),
		`INSERT INTO listings (id, seller_id, title, description, category, condition, cover_image_url, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())`,
		listingID, userID, req.Title, req.Description, req.Category, req.Condition, req.CoverImageURL,
	)
	if err != nil {
		http.Error(w, "failed to create listing", http.StatusInternalServerError)
		return
	}

	var listing ListingResponse
	err = h.store.DB.QueryRowContext(r.Context(),
		`SELECT id, seller_id, title, description, category, condition, cover_image_url FROM listings WHERE id = $1`,
		listingID,
	).Scan(&listing.ID, &listing.SellerID, &listing.Title, &listing.Description, &listing.Category, &listing.Condition, &listing.CoverImageURL)
	if err != nil {
		http.Error(w, "failed to fetch listing", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(listing)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request, listingID string) {
	var listing ListingResponse
	err := h.store.DB.QueryRowContext(r.Context(),
		`SELECT id, seller_id, title, description, category, condition, COALESCE(cover_image_url, '') FROM listings WHERE id = $1`,
		listingID,
	).Scan(&listing.ID, &listing.SellerID, &listing.Title, &listing.Description, &listing.Category, &listing.Condition, &listing.CoverImageURL)
	if err != nil {
		http.Error(w, "listing not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listing)
}

type ListListingsResponse struct {
	Listings []ListingResponse `json:"listings"`
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	category := query.Get("category")
	sellerID := query.Get("seller_id")

	sql := `SELECT id, seller_id, title, description, category, condition, COALESCE(cover_image_url, '')
			FROM listings WHERE 1=1`
	args := []interface{}{}
	argIdx := 1

	if category != "" {
		sql += fmt.Sprintf(" AND category = $%d", argIdx)
		args = append(args, category)
		argIdx++
	}

	if sellerID != "" {
		sql += fmt.Sprintf(" AND seller_id = $%d", argIdx)
		args = append(args, sellerID)
		argIdx++
	}

	sql += " ORDER BY created_at DESC LIMIT 50"

	rows, err := h.store.DB.QueryContext(r.Context(), sql, args...)
	if err != nil {
		http.Error(w, "failed to fetch listings", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var listings []ListingResponse
	for rows.Next() {
		var listing ListingResponse
		if err := rows.Scan(&listing.ID, &listing.SellerID, &listing.Title, &listing.Description, &listing.Category, &listing.Condition, &listing.CoverImageURL); err != nil {
			http.Error(w, "failed to scan listing", http.StatusInternalServerError)
			return
		}
		listings = append(listings, listing)
	}

	if listings == nil {
		listings = []ListingResponse{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListListingsResponse{Listings: listings})
}

type UpdateListingRequest struct {
	Title          *string `json:"title,omitempty"`
	Description    *string `json:"description,omitempty"`
	Category       *string `json:"category,omitempty"`
	Condition      *string `json:"condition,omitempty"`
	CoverImageURL  *string `json:"cover_image_url,omitempty"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request, listingID string) {
	userID := auth.GetUserID(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var sellerID string
	err := h.store.DB.QueryRowContext(r.Context(), `SELECT seller_id FROM listings WHERE id = $1`, listingID).Scan(&sellerID)
	if err != nil {
		http.Error(w, "listing not found", http.StatusNotFound)
		return
	}

	if sellerID != userID {
		http.Error(w, "only the seller can update this listing", http.StatusForbidden)
		return
	}

	var req UpdateListingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	query := `UPDATE listings SET updated_at = NOW()`
	args := []interface{}{}
	argIdx := 1

	if req.Title != nil {
		query += fmt.Sprintf(", title = $%d", argIdx)
		args = append(args, *req.Title)
		argIdx++
	}
	if req.Description != nil {
		query += fmt.Sprintf(", description = $%d", argIdx)
		args = append(args, *req.Description)
		argIdx++
	}
	if req.Category != nil {
		query += fmt.Sprintf(", category = $%d", argIdx)
		args = append(args, *req.Category)
		argIdx++
	}
	if req.Condition != nil {
		query += fmt.Sprintf(", condition = $%d", argIdx)
		args = append(args, *req.Condition)
		argIdx++
	}
	if req.CoverImageURL != nil {
		query += fmt.Sprintf(", cover_image_url = $%d", argIdx)
		args = append(args, *req.CoverImageURL)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, listingID)

	_, err = h.store.DB.ExecContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "failed to update listing", http.StatusInternalServerError)
		return
	}

	var listing ListingResponse
	err = h.store.DB.QueryRowContext(r.Context(),
		`SELECT id, seller_id, title, description, category, condition, COALESCE(cover_image_url, '') FROM listings WHERE id = $1`,
		listingID,
	).Scan(&listing.ID, &listing.SellerID, &listing.Title, &listing.Description, &listing.Category, &listing.Condition, &listing.CoverImageURL)
	if err != nil {
		http.Error(w, "failed to fetch listing", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listing)
}
