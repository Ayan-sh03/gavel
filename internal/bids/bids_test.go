package bids_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bidding/internal/auth"
	"bidding/internal/bids"
	"bidding/internal/db"

	nat "github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

func TestPlaceBid(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller@example.com")
	bidderID := createTestUser(t, store, "bidder@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, time.Now().UTC().Add(-1*time.Hour), time.Now().UTC().Add(1*time.Hour))

	handler := bids.NewHandler(store)

	reqBody := map[string]interface{}{
		"amount_cents": 10500,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), bidderID))
	rec := httptest.NewRecorder()

	handler.PlaceBid(rec, req, auctionID)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["accepted"] != true {
		t.Fatalf("expected bid to be accepted, got %v", resp)
	}

	if resp["current_price_cents"] != float64(10500) {
		t.Fatalf("expected current_price_cents to be 10500, got %v", resp["current_price_cents"])
	}
}

func setupTestDB(t *testing.T, ctx context.Context) *db.Store {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "auction_test",
			"POSTGRES_USER":     "auction",
			"POSTGRES_PASSWORD": "auction",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp"),
	}

	pgContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = pgContainer.Terminate(ctx)
	})

	host, _ := pgContainer.Host(ctx)
	ports, _ := pgContainer.Ports(ctx)

	var firstPort nat.Port
	for p := range ports {
		firstPort = p
		break
	}

	mapped, _ := pgContainer.MappedPort(ctx, firstPort)
	dsn := fmt.Sprintf("postgres://auction:auction@%s:%s/auction_test?sslmode=disable", host, mapped.Port())

	store, err := db.Open(ctx, dsn, "../../internal/db/migrations")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	return store
}

func createTestUser(t *testing.T, store *db.Store, email string) string {
	t.Helper()

	hash, _ := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
	userID := "user-" + email

	_, err := store.DB.Exec(
		`INSERT INTO users (id, email, password_hash, display_name, role, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		userID, email, string(hash), "Test User", "user", "active",
	)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	return userID
}

func createTestListing(t *testing.T, store *db.Store, userID string) string {
	t.Helper()

	listingID := "test-listing-id"

	_, err := store.DB.Exec(
		`INSERT INTO listings (id, seller_id, title, description, category, condition, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		listingID, userID, "Test Item", "Test description", "electronics", "new",
	)
	if err != nil {
		t.Fatalf("failed to create test listing: %v", err)
	}

	return listingID
}

func createTestAuction(t *testing.T, store *db.Store, listingID string, startsAt, endsAt time.Time) string {
	t.Helper()

	auctionID := "test-auction-id"

	_, err := store.DB.Exec(
		`INSERT INTO auctions (id, listing_id, status, currency, starting_price_cents, min_increment_cents, 
			starts_at, ends_at, extension_window_secs, bid_count, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())`,
		auctionID, listingID, "live", "USD", 10000, 500, startsAt, endsAt, 180, 0,
	)
	if err != nil {
		t.Fatalf("failed to create test auction: %v", err)
	}

	return auctionID
}

func TestAntiSniping(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller2@example.com")
	bidderID := createTestUser(t, store, "bidder2@example.com")
	listingID := createTestListing(t, store, sellerID)
	
	endsAt := time.Now().UTC().Add(2 * time.Minute)
	auctionID := createTestAuction(t, store, listingID, time.Now().UTC().Add(-1*time.Hour), endsAt)

	handler := bids.NewHandler(store)

	reqBody := map[string]interface{}{
		"amount_cents": 10500,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), bidderID))
	rec := httptest.NewRecorder()

	handler.PlaceBid(rec, req, auctionID)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)

	endsAtStr := resp["ends_at"].(string)
	newEndsAt, _ := time.Parse(time.RFC3339, endsAtStr)

	if !newEndsAt.After(endsAt) {
		t.Fatalf("expected auction end time to be extended, original: %v, new: %v", endsAt, newEndsAt)
	}
}

func TestBidTooLow(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller3@example.com")
	bidderID := createTestUser(t, store, "bidder3@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, time.Now().UTC().Add(-1*time.Hour), time.Now().UTC().Add(1*time.Hour))

	handler := bids.NewHandler(store)

	reqBody := map[string]interface{}{
		"amount_cents": 10100,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), bidderID))
	rec := httptest.NewRecorder()

	handler.PlaceBid(rec, req, auctionID)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp["accepted"] != false {
		t.Fatalf("expected bid to be rejected, got %v", resp)
	}

	if resp["next_min_cents"] != float64(10500) {
		t.Fatalf("expected next_min_cents to be 10500, got %v", resp["next_min_cents"])
	}
}

func TestSellerCannotBid(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller4@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, time.Now().UTC().Add(-1*time.Hour), time.Now().UTC().Add(1*time.Hour))

	handler := bids.NewHandler(store)

	reqBody := map[string]interface{}{
		"amount_cents": 10500,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), sellerID))
	rec := httptest.NewRecorder()

	handler.PlaceBid(rec, req, auctionID)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller5@example.com")
	bidderID := createTestUser(t, store, "bidder5@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, time.Now().UTC().Add(-1*time.Hour), time.Now().UTC().Add(1*time.Hour))

	handler := bids.NewHandler(store)

	reqBody := map[string]interface{}{
		"amount_cents": 10500,
	}
	body, _ := json.Marshal(reqBody)

	req1 := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", "test-key-123")
	req1 = req1.WithContext(context.WithValue(req1.Context(), auth.GetUserIDKey(), bidderID))
	rec1 := httptest.NewRecorder()

	handler.PlaceBid(rec1, req1, auctionID)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	body2, _ := json.Marshal(reqBody)
	req2 := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "test-key-123")
	req2 = req2.WithContext(context.WithValue(req2.Context(), auth.GetUserIDKey(), bidderID))
	rec2 := httptest.NewRecorder()

	handler.PlaceBid(rec2, req2, auctionID)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected status 201 for retry, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var bidCount int
	store.DB.QueryRow(`SELECT COUNT(*) FROM bids WHERE auction_id = $1 AND bidder_id = $2`, auctionID, bidderID).Scan(&bidCount)

	if bidCount != 1 {
		t.Fatalf("expected only 1 bid to be created, got %d", bidCount)
	}
}

func TestGetBids(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller6@example.com")
	bidderID := createTestUser(t, store, "bidder6@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, time.Now().UTC().Add(-1*time.Hour), time.Now().UTC().Add(1*time.Hour))

	createTestBid(t, store, auctionID, bidderID, 10500)

	handler := bids.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/auctions/"+auctionID+"/bids", nil)
	rec := httptest.NewRecorder()

	handler.GetBids(rec, req, auctionID)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	bids, ok := resp["bids"].([]interface{})
	if !ok || len(bids) == 0 {
		t.Fatalf("expected non-empty bids array, got %v", resp)
	}
}

func createTestBid(t *testing.T, store *db.Store, auctionID, bidderID string, amountCents int64) {
	t.Helper()

	bidID := uuid.New().String()

	_, err := store.DB.Exec(
		`INSERT INTO bids (id, auction_id, bidder_id, amount_cents, placed_at, accepted)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		bidID, auctionID, bidderID, amountCents, time.Now().UTC(), true,
	)
	if err != nil {
		t.Fatalf("failed to create test bid: %v", err)
	}
}
