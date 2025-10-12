package listings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"bidding/internal/auth"
	"bidding/internal/db"
	"bidding/internal/listings"

	nat "github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateListing(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	userID := createTestUser(t, store)

	handler := listings.NewHandler(store)

	reqBody := map[string]interface{}{
		"title":           "Test Item",
		"description":     "Test description",
		"category":        "electronics",
		"condition":       "new",
		"cover_image_url": "https://example.com/image.jpg",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/listings", bytes.NewReader(body))
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

	if resp["id"] == nil || resp["title"] != "Test Item" {
		t.Fatalf("expected listing response, got %v", resp)
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

func TestGetListing(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	userID := createTestUser(t, store)
	listingID := createTestListing(t, store, userID)

	handler := listings.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/listings/"+listingID, nil)
	rec := httptest.NewRecorder()

	handler.Get(rec, req, listingID)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["id"] != listingID {
		t.Fatalf("expected listing id %s, got %v", listingID, resp["id"])
	}
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

func TestListListings(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	userID := createTestUser(t, store)
	createTestListing(t, store, userID)

	handler := listings.NewHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/listings", nil)
	rec := httptest.NewRecorder()

	handler.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	listings, ok := resp["listings"].([]interface{})
	if !ok || len(listings) == 0 {
		t.Fatalf("expected non-empty listings array, got %v", resp)
	}
}

func TestUpdateListing(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	userID := createTestUser(t, store)
	listingID := createTestListing(t, store, userID)

	handler := listings.NewHandler(store)

	reqBody := map[string]interface{}{
		"title":       "Updated Title",
		"description": "Updated description",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPatch, "/listings/"+listingID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), userID))
	rec := httptest.NewRecorder()

	handler.Update(rec, req, listingID)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["title"] != "Updated Title" {
		t.Fatalf("expected title to be updated, got %v", resp["title"])
	}
}
