package auctions_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"bidding/internal/auctions"
	"bidding/internal/auth"
)

func TestCancelAuction(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store)
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID)

	handler := auctions.NewHandler(store)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/cancel", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), sellerID))
	rec := httptest.NewRecorder()

	handler.Cancel(rec, req, auctionID)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var status string
	store.DB.QueryRow(`SELECT status FROM auctions WHERE id = $1`, auctionID).Scan(&status)

	if status != "canceled" {
		t.Fatalf("expected auction status to be canceled, got %s", status)
	}
}

func TestCancelAuctionWithBids(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store)
	bidderID := "bidder-id"
	store.DB.Exec(`INSERT INTO users (id, email, password_hash, display_name, role, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`, bidderID, "bidder@example.com", "hash", "Bidder", "user", "active")

	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID)

	store.DB.Exec(`UPDATE auctions SET current_winner_id = $1, current_price_cents = 15000, bid_count = 1 WHERE id = $2`, bidderID, auctionID)

	handler := auctions.NewHandler(store)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/cancel", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), sellerID))
	rec := httptest.NewRecorder()

	handler.Cancel(rec, req, auctionID)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 (cannot cancel with bids), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateAuctionAsAdmin(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	adminID := "admin-id"
	store.DB.Exec(`INSERT INTO users (id, email, password_hash, display_name, role, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`, adminID, "admin@example.com", "hash", "Admin", "admin", "active")

	sellerID := createTestUser(t, store)
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID)

	handler := auctions.NewHandler(store)

	reqBody := `{"status": "canceled"}`
	req := httptest.NewRequest(http.MethodPatch, "/auctions/"+auctionID, bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), adminID))
	rec := httptest.NewRecorder()

	handler.Update(rec, req, auctionID)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var status string
	store.DB.QueryRow(`SELECT status FROM auctions WHERE id = $1`, auctionID).Scan(&status)

	if status != "canceled" {
		t.Fatalf("expected auction status to be canceled, got %s", status)
	}
}

func TestUpdateAuctionNonAdmin(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store)
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID)

	handler := auctions.NewHandler(store)

	reqBody := `{"status": "canceled"}`
	req := httptest.NewRequest(http.MethodPatch, "/auctions/"+auctionID, bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), sellerID))
	rec := httptest.NewRecorder()

	handler.Update(rec, req, auctionID)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 (non-admin cannot update), got %d: %s", rec.Code, rec.Body.String())
	}
}
