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
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

func BenchmarkPlaceBid(b *testing.B) {
	ctx := context.Background()
	store := setupBenchDB(b, ctx)
	defer store.Close()

	sellerID := createBenchUser(b, store, "seller@bench.com")
	bidderID := createBenchUser(b, store, "bidder@bench.com")
	listingID := createBenchListing(b, store, sellerID)
	auctionID := createBenchAuction(b, store, listingID)

	handler := bids.NewHandler(store)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		reqBody := map[string]interface{}{
			"amount_cents": 10000 + int64(i*100),
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), bidderID))
		rec := httptest.NewRecorder()

		handler.PlaceBid(rec, req, auctionID)
	}
}

func BenchmarkPlaceBidConcurrent(b *testing.B) {
	ctx := context.Background()
	store := setupBenchDB(b, ctx)
	defer store.Close()

	sellerID := createBenchUser(b, store, "seller2@bench.com")
	listingID := createBenchListing(b, store, sellerID)
	auctionID := createBenchAuction(b, store, listingID)

	// Create multiple bidders
	bidders := make([]string, 10)
	for i := 0; i < 10; i++ {
		bidders[i] = createBenchUser(b, store, fmt.Sprintf("bidder%d@bench.com", i))
	}

	handler := bids.NewHandler(store)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		bidderIdx := 0
		bidAmount := int64(10000)
		
		for pb.Next() {
			reqBody := map[string]interface{}{
				"amount_cents": bidAmount,
			}
			bidAmount += 100
			body, _ := json.Marshal(reqBody)

			req := httptest.NewRequest(http.MethodPost, "/auctions/"+auctionID+"/bids", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), bidders[bidderIdx%len(bidders)]))
			rec := httptest.NewRecorder()

			handler.PlaceBid(rec, req, auctionID)
			bidderIdx++
		}
	})
}

func setupBenchDB(b *testing.B, ctx context.Context) *db.Store {
	b.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "auction_bench",
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
		b.Fatalf("failed to start postgres: %v", err)
	}

	b.Cleanup(func() {
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
	dsn := fmt.Sprintf("postgres://auction:auction@%s:%s/auction_bench?sslmode=disable", host, mapped.Port())

	store, err := db.Open(ctx, dsn, "../../internal/db/migrations")
	if err != nil {
		b.Fatalf("failed to open database: %v", err)
	}

	return store
}

func createBenchUser(b *testing.B, store *db.Store, email string) string {
	b.Helper()

	hash, _ := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
	userID := "user-" + email

	_, err := store.DB.Exec(
		`INSERT INTO users (id, email, password_hash, display_name, role, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		userID, email, string(hash), "Bench User", "user", "active",
	)
	if err != nil {
		b.Fatalf("failed to create user: %v", err)
	}

	return userID
}

func createBenchListing(b *testing.B, store *db.Store, userID string) string {
	b.Helper()

	listingID := "listing-" + userID

	_, err := store.DB.Exec(
		`INSERT INTO listings (id, seller_id, title, description, category, condition, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		listingID, userID, "Bench Item", "Description", "electronics", "new",
	)
	if err != nil {
		b.Fatalf("failed to create listing: %v", err)
	}

	return listingID
}

func createBenchAuction(b *testing.B, store *db.Store, listingID string) string {
	b.Helper()

	auctionID := "auction-" + listingID

	_, err := store.DB.Exec(
		`INSERT INTO auctions (id, listing_id, status, currency, starting_price_cents, min_increment_cents, 
			starts_at, ends_at, extension_window_secs, bid_count, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())`,
		auctionID, listingID, "live", "USD", 10000, 100,
		time.Now().UTC().Add(-1*time.Hour), time.Now().UTC().Add(24*time.Hour), 180, 0,
	)
	if err != nil {
		b.Fatalf("failed to create auction: %v", err)
	}

	return auctionID
}
