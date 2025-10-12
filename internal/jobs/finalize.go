package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"bidding/internal/db"

	"github.com/google/uuid"
)

type AuctionFinalizer struct {
	store *db.Store
}

func NewAuctionFinalizer(store *db.Store) *AuctionFinalizer {
	return &AuctionFinalizer{store: store}
}

func (f *AuctionFinalizer) ClaimDueAuction(ctx context.Context) (*string, error) {
	var auctionID string
	err := f.store.DB.QueryRowContext(ctx,
		`UPDATE auctions
		 SET status = 'ending', updated_at = NOW()
		 WHERE id = (
			SELECT id FROM auctions
			WHERE status = 'live' AND ends_at <= NOW()
			ORDER BY ends_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id`,
	).Scan(&auctionID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to claim auction: %w", err)
	}

	return &auctionID, nil
}

func (f *AuctionFinalizer) FinalizeAuction(ctx context.Context, auctionID string) error {
	tx, err := f.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	var auction struct {
		ListingID         string
		CurrentWinnerID   sql.NullString
		CurrentPriceCents sql.NullInt64
		ReservePriceCents sql.NullInt64
	}

	err = tx.QueryRowContext(ctx,
		`SELECT listing_id, current_winner_id, current_price_cents, reserve_price_cents
		 FROM auctions WHERE id = $1 FOR UPDATE`,
		auctionID,
	).Scan(&auction.ListingID, &auction.CurrentWinnerID, &auction.CurrentPriceCents, &auction.ReservePriceCents)

	if err != nil {
		return fmt.Errorf("failed to fetch auction: %w", err)
	}

	var sellerID string
	err = tx.QueryRowContext(ctx,
		`SELECT seller_id FROM listings WHERE id = $1`,
		auction.ListingID,
	).Scan(&sellerID)

	if err != nil {
		return fmt.Errorf("failed to fetch seller: %w", err)
	}

	hasWinner := auction.CurrentWinnerID.Valid && auction.CurrentPriceCents.Valid
	reserveMet := true

	if auction.ReservePriceCents.Valid && auction.CurrentPriceCents.Valid {
		reserveMet = auction.CurrentPriceCents.Int64 >= auction.ReservePriceCents.Int64
	}

	if !hasWinner || !reserveMet {
		_, err = tx.ExecContext(ctx,
			`UPDATE auctions SET status = 'ended', updated_at = NOW() WHERE id = $1`,
			auctionID,
		)
		if err != nil {
			return fmt.Errorf("failed to mark auction as ended (unsold): %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}
		return nil
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE auctions SET status = 'ended', updated_at = NOW() WHERE id = $1`,
		auctionID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark auction as ended: %w", err)
	}

	paymentID := uuid.New().String()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO payments (id, auction_id, winner_id, amount_cents, status, provider, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		paymentID, auctionID, auction.CurrentWinnerID.String, auction.CurrentPriceCents.Int64, "pending", "manual",
	)
	if err != nil {
		return fmt.Errorf("failed to create payment: %w", err)
	}

	payoutID := uuid.New().String()
	payoutAmount := auction.CurrentPriceCents.Int64
	_, err = tx.ExecContext(ctx,
		`INSERT INTO payouts (id, auction_id, seller_id, amount_cents, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW(), NOW())`,
		payoutID, auctionID, sellerID, payoutAmount, "pending",
	)
	if err != nil {
		return fmt.Errorf("failed to create payout: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (f *AuctionFinalizer) Run(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			auctionID, err := f.ClaimDueAuction(ctx)
			if err != nil {
				fmt.Printf("error claiming auction: %v\n", err)
				continue
			}

			if auctionID == nil {
				continue
			}

			if err := f.FinalizeAuction(ctx, *auctionID); err != nil {
				fmt.Printf("error finalizing auction %s: %v\n", *auctionID, err)
			}
		}
	}
}
