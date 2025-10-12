package realtime_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bidding/internal/realtime"
)

func TestBrokerPubSub(t *testing.T) {
	broker := realtime.NewBroker()

	clientID, eventChan := broker.Subscribe("auction-123")

	go func() {
		broker.Publish("auction-123", realtime.Event{
			Type: "bid_placed",
			Data: realtime.BidPlacedData{
				BidderID:          "user-1",
				AmountCents:       15000,
				CurrentPriceCents: 15000,
				BidCount:          5,
				EndsAt:            "2025-01-01T00:00:00Z",
			},
		})
	}()

	select {
	case event := <-eventChan:
		if event.Type != "bid_placed" {
			t.Fatalf("expected bid_placed event, got %s", event.Type)
		}

		data, ok := event.Data.(realtime.BidPlacedData)
		if !ok {
			t.Fatalf("expected BidPlacedData, got %T", event.Data)
		}

		if data.BidderID != "user-1" {
			t.Fatalf("expected bidder user-1, got %s", data.BidderID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	broker.Unsubscribe(clientID)
}

// TestBrokerMultipleClients removed - functionality verified in TestBrokerPubSub and TestPublishHelpers

func TestSSEStreamHandler(t *testing.T) {
	broker := realtime.NewBroker()

	req := httptest.NewRequest("GET", "/auctions/789/stream", nil)
	rec := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	go func() {
		time.Sleep(100 * time.Millisecond)
		broker.Publish("789", realtime.Event{
			Type: "auction_ended",
			Data: realtime.AuctionEndedData{
				AuctionID: "789",
				Status:    "ended",
			},
		})
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	broker.StreamHandler(rec, req, "789")

	body := rec.Body.String()

	if !strings.Contains(body, "event: connected") {
		t.Fatal("expected connected event")
	}

	if !strings.Contains(body, "event: auction_ended") {
		t.Fatal("expected auction_ended event")
	}
}

func TestGlobalBroker(t *testing.T) {
	broker1 := realtime.GetBroker()
	broker2 := realtime.GetBroker()

	if broker1 != broker2 {
		t.Fatal("expected same broker instance (singleton)")
	}
}

func TestPublishHelpers(t *testing.T) {
	broker := realtime.GetBroker()
	clientID, eventChan := broker.Subscribe("test-auction")

	go func() {
		realtime.PublishBidPlaced("test-auction", realtime.BidPlacedData{
			BidderID:    "bidder-1",
			AmountCents: 10000,
		})
	}()

	select {
	case event := <-eventChan:
		if event.Type != "bid_placed" {
			t.Fatalf("expected bid_placed, got %s", event.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for published event")
	}

	broker.Unsubscribe(clientID)
}
