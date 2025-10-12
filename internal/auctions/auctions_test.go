package auctions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bidding/internal/auctions"
	"bidding/internal/auth"
	"bidding/internal/db"

	nat "github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateAuction(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	userID := createTestUser(t, store)
	listingID := createTestListing(t, store, userID)

	handler := auctions.NewHandler(store)

	startsAt := time.Now().UTC().Add(1 * time.Hour)
	endsAt := startsAt.Add(24 * time.Hour)

	reqBody := map[string]interface{}{
		"listing_id":          listingID,
		"currency":            "USD",
		"starting_price_cents": 10000,
		"min_increment_cents":  500,
		"starts_at":           startsAt.Format(time.RFC3339),
		"ends_at":             endsAt.Format(time.RFC3339),
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auctions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), userID))
	rec := httptest.NewRecorder()

	handler.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["id"] == nil || resp["status"] != "scheduled" {
		t.Fatalf("expected auction response with status scheduled, got %v", resp)
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

func createTestUser(t *testing.T, store *db.Store) string {
	t.Helper()

	hash, _ := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
	userID := "test-user-id"

	_, err := store.DB.Exec(
		`INSERT INTO users (id, email, password_hash, display_name, role, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		userID, "test@example.com", string(hash), "Test User", "user", "active",
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

func TestGetAuction(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	userID := createTestUser(t, store)
	listingID := createTestListing(t, store, userID)
	auctionID := createTestAuction(t, store, listingID)

	handler := auctions.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/auctions/"+auctionID, nil)
	rec := httptest.NewRecorder()

	handler.Get(rec, req, auctionID)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["id"] != auctionID {
		t.Fatalf("expected auction id %s, got %v", auctionID, resp["id"])
	}
}

func createTestAuction(t *testing.T, store *db.Store, listingID string) string {
	t.Helper()

	auctionID := "test-auction-id"
	startsAt := time.Now().UTC().Add(1 * time.Hour)
	endsAt := startsAt.Add(24 * time.Hour)

	_, err := store.DB.Exec(
		`INSERT INTO auctions (id, listing_id, status, currency, starting_price_cents, min_increment_cents, 
			starts_at, ends_at, extension_window_secs, bid_count, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())`,
		auctionID, listingID, "scheduled", "USD", 10000, 500, startsAt, endsAt, 180, 0,
	)
	if err != nil {
		t.Fatalf("failed to create test auction: %v", err)
	}

	return auctionID
}

func TestListAuctions(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	userID := createTestUser(t, store)
	listingID := createTestListing(t, store, userID)
	createTestAuction(t, store, listingID)

	handler := auctions.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/auctions?status=scheduled", nil)
	rec := httptest.NewRecorder()

	handler.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	auctions, ok := resp["auctions"].([]interface{})
	if !ok || len(auctions) == 0 {
		t.Fatalf("expected non-empty auctions array, got %v", resp)
	}
}
