package audit

import (
	"context"
	"encoding/json"

	"bidding/internal/db"

	"github.com/google/uuid"
)

type Logger struct {
	store *db.Store
}

func NewLogger(store *db.Store) *Logger {
	return &Logger{store: store}
}

type Entry struct {
	ActorID     *string
	Action      string
	SubjectType string
	SubjectID   string
	Meta        map[string]interface{}
}

func (l *Logger) Log(ctx context.Context, entry Entry) error {
	var metaJSON []byte
	var err error

	if entry.Meta != nil {
		metaJSON, err = json.Marshal(entry.Meta)
		if err != nil {
			return err
		}
	}

	_, err = l.store.DB.ExecContext(ctx,
		`INSERT INTO audit_log (id, actor_id, action, subject_type, subject_id, meta_json, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		uuid.New().String(), entry.ActorID, entry.Action, entry.SubjectType, entry.SubjectID, metaJSON,
	)

	return err
}

// Helper functions for common audit actions
func LogBidPlaced(ctx context.Context, store *db.Store, userID, auctionID string, amountCents int64) {
	logger := NewLogger(store)
	logger.Log(ctx, Entry{
		ActorID:     &userID,
		Action:      "bid_placed",
		SubjectType: "auction",
		SubjectID:   auctionID,
		Meta: map[string]interface{}{
			"amount_cents": amountCents,
		},
	})
}

func LogAuctionCreated(ctx context.Context, store *db.Store, userID, auctionID string) {
	logger := NewLogger(store)
	logger.Log(ctx, Entry{
		ActorID:     &userID,
		Action:      "auction_created",
		SubjectType: "auction",
		SubjectID:   auctionID,
	})
}

func LogAuctionCanceled(ctx context.Context, store *db.Store, userID, auctionID string) {
	logger := NewLogger(store)
	logger.Log(ctx, Entry{
		ActorID:     &userID,
		Action:      "auction_canceled",
		SubjectType: "auction",
		SubjectID:   auctionID,
	})
}

func LogUserUpdated(ctx context.Context, store *db.Store, adminID, targetUserID string, changes map[string]interface{}) {
	logger := NewLogger(store)
	logger.Log(ctx, Entry{
		ActorID:     &adminID,
		Action:      "user_updated",
		SubjectType: "user",
		SubjectID:   targetUserID,
		Meta:        changes,
	})
}

func LogPaymentCompleted(ctx context.Context, store *db.Store, userID, auctionID string, amountCents int64) {
	logger := NewLogger(store)
	logger.Log(ctx, Entry{
		ActorID:     &userID,
		Action:      "payment_completed",
		SubjectType: "auction",
		SubjectID:   auctionID,
		Meta: map[string]interface{}{
			"amount_cents": amountCents,
		},
	})
}
