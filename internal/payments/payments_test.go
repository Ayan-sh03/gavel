package payments_test

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
	"bidding/internal/db"
	"bidding/internal/payments"

	nat "github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

func TestPayForAuction(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller@example.com")
	winnerID := createTestUser(t, store, "winner@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID)

	store.DB.Exec(`UPDATE auctions SET status = 'ended', current_winner_id = $1, current_price_cents = 15000 WHERE id = $2`, winnerID, auctionID)

	handler := payments.NewHandler(store)

	reqBody := map[string]string{"payment_method": "credit_card"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/pay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), winnerID))
	rec := httptest.NewRecorder()

	handler.Pay(rec, req, auctionID)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "completed" {
		t.Fatalf("expected payment status to be completed, got %v", resp["status"])
	}

	if resp["amount_cents"].(float64) != 15000 {
		t.Fatalf("expected amount 15000, got %v", resp["amount_cents"])
	}
}

func TestPayForAuctionNotWinner(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller2@example.com")
	winnerID := createTestUser(t, store, "winner2@example.com")
	otherUserID := createTestUser(t, store, "other2@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID)

	store.DB.Exec(`UPDATE auctions SET status = 'ended', current_winner_id = $1, current_price_cents = 15000 WHERE id = $2`, winnerID, auctionID)

	handler := payments.NewHandler(store)

	reqBody := map[string]string{"payment_method": "credit_card"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/pay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), otherUserID))
	rec := httptest.NewRecorder()

	handler.Pay(rec, req, auctionID)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetPayment(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller3@example.com")
	winnerID := createTestUser(t, store, "winner3@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID)

	paymentID := "payment-123"
	store.DB.Exec(`INSERT INTO payments (id, auction_id, winner_id, amount_cents, status, provider, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`, paymentID, auctionID, winnerID, 15000, "completed", "mock")

	handler := payments.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/payments/"+paymentID, nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), winnerID))
	rec := httptest.NewRecorder()

	handler.GetPayment(rec, req, paymentID)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["id"] != paymentID {
		t.Fatalf("expected payment id %s, got %v", paymentID, resp["id"])
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

	listingID := "listing-" + userID

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

func createTestAuction(t *testing.T, store *db.Store, listingID string) string {
	t.Helper()

	auctionID := "auction-" + listingID

	_, err := store.DB.Exec(
		`INSERT INTO auctions (id, listing_id, status, currency, starting_price_cents, min_increment_cents, 
			starts_at, ends_at, extension_window_secs, bid_count, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())`,
		auctionID, listingID, "live", "USD", 10000, 500,
		time.Now().UTC().Add(-1*time.Hour), time.Now().UTC().Add(1*time.Hour), 180, 0,
	)
	if err != nil {
		t.Fatalf("failed to create test auction: %v", err)
	}

	return auctionID
}
