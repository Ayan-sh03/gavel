package jobs_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"bidding/internal/db"
	"bidding/internal/jobs"

	nat "github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"
)

func TestClaimDueAuction(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, sellerID, time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-1*time.Minute))

	finalizer := jobs.NewAuctionFinalizer(store)

	claimed, err := finalizer.ClaimDueAuction(ctx)
	if err != nil {
		t.Fatalf("failed to claim auction: %v", err)
	}

	if claimed == nil {
		t.Fatal("expected to claim an auction, got nil")
	}

	if *claimed != auctionID {
		t.Fatalf("expected to claim auction %s, got %s", auctionID, *claimed)
	}

	var status string
	store.DB.QueryRow(`SELECT status FROM auctions WHERE id = $1`, auctionID).Scan(&status)

	if status != "ending" {
		t.Fatalf("expected auction status to be 'ending', got %s", status)
	}
}

func TestFinalizeAuctionWithWinner(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller2@example.com")
	bidderID := createTestUser(t, store, "bidder2@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, sellerID, time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-1*time.Minute))
	
	updateAuctionWithWinner(t, store, auctionID, bidderID, 15000)
	store.DB.Exec(`UPDATE auctions SET status = 'ending' WHERE id = $1`, auctionID)

	finalizer := jobs.NewAuctionFinalizer(store)

	err := finalizer.FinalizeAuction(ctx, auctionID)
	if err != nil {
		t.Fatalf("failed to finalize auction: %v", err)
	}

	var status string
	store.DB.QueryRow(`SELECT status FROM auctions WHERE id = $1`, auctionID).Scan(&status)

	if status != "ended" {
		t.Fatalf("expected auction status to be 'ended', got %s", status)
	}

	var paymentCount int
	store.DB.QueryRow(`SELECT COUNT(*) FROM payments WHERE auction_id = $1`, auctionID).Scan(&paymentCount)

	if paymentCount != 1 {
		t.Fatalf("expected 1 payment record, got %d", paymentCount)
	}

	var payoutCount int
	store.DB.QueryRow(`SELECT COUNT(*) FROM payouts WHERE auction_id = $1`, auctionID).Scan(&payoutCount)

	if payoutCount != 1 {
		t.Fatalf("expected 1 payout record, got %d", payoutCount)
	}
}

func TestFinalizeAuctionWithoutWinner(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	sellerID := createTestUser(t, store, "seller3@example.com")
	listingID := createTestListing(t, store, sellerID)
	auctionID := createTestAuction(t, store, listingID, sellerID, time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-1*time.Minute))
	
	store.DB.Exec(`UPDATE auctions SET status = 'ending' WHERE id = $1`, auctionID)

	finalizer := jobs.NewAuctionFinalizer(store)

	err := finalizer.FinalizeAuction(ctx, auctionID)
	if err != nil {
		t.Fatalf("failed to finalize auction: %v", err)
	}

	var status string
	store.DB.QueryRow(`SELECT status FROM auctions WHERE id = $1`, auctionID).Scan(&status)

	if status != "ended" {
		t.Fatalf("expected auction status to be 'ended', got %s", status)
	}

	var paymentCount int
	store.DB.QueryRow(`SELECT COUNT(*) FROM payments WHERE auction_id = $1`, auctionID).Scan(&paymentCount)

	if paymentCount != 0 {
		t.Fatalf("expected 0 payment records for unsold auction, got %d", paymentCount)
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

func createTestAuction(t *testing.T, store *db.Store, listingID, sellerID string, startsAt, endsAt time.Time) string {
	t.Helper()

	auctionID := "auction-" + listingID

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

func updateAuctionWithWinner(t *testing.T, store *db.Store, auctionID, winnerID string, priceCents int64) {
	t.Helper()

	_, err := store.DB.Exec(
		`UPDATE auctions SET current_winner_id = $1, current_price_cents = $2, bid_count = 1 WHERE id = $3`,
		winnerID, priceCents, auctionID,
	)
	if err != nil {
		t.Fatalf("failed to update auction with winner: %v", err)
	}
}
