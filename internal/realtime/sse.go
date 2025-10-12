package realtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Event struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type BidPlacedData struct {
	BidderID          string `json:"bidder_id"`
	AmountCents       int64  `json:"amount_cents"`
	CurrentPriceCents int64  `json:"current_price_cents"`
	BidCount          int    `json:"bid_count"`
	EndsAt            string `json:"ends_at"`
}

type TimeExtendedData struct {
	NewEndsAt string `json:"new_ends_at"`
	ExtendedBy int   `json:"extended_by_seconds"`
}

type AuctionEndedData struct {
	AuctionID string  `json:"auction_id"`
	Status    string  `json:"status"`
	WinnerID  *string `json:"winner_id,omitempty"`
	FinalPrice *int64 `json:"final_price_cents,omitempty"`
}

type client struct {
	id       string
	auctionID string
	channel  chan Event
}

type Broker struct {
	mu          sync.RWMutex
	clients     map[string]*client
	subscribers map[string]map[string]*client // auctionID -> clientID -> client
}

func NewBroker() *Broker {
	return &Broker{
		clients:     make(map[string]*client),
		subscribers: make(map[string]map[string]*client),
	}
}

func (b *Broker) Subscribe(auctionID string) (string, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	clientID := fmt.Sprintf("%s-%d", auctionID, time.Now().UnixNano())
	c := &client{
		id:        clientID,
		auctionID: auctionID,
		channel:   make(chan Event, 10),
	}

	b.clients[clientID] = c

	if b.subscribers[auctionID] == nil {
		b.subscribers[auctionID] = make(map[string]*client)
	}
	b.subscribers[auctionID][clientID] = c

	return clientID, c.channel
}

func (b *Broker) Unsubscribe(clientID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	c, exists := b.clients[clientID]
	if !exists {
		return
	}

	delete(b.clients, clientID)
	delete(b.subscribers[c.auctionID], clientID)

	if len(b.subscribers[c.auctionID]) == 0 {
		delete(b.subscribers, c.auctionID)
	}

	close(c.channel)
}

func (b *Broker) Publish(auctionID string, event Event) {
	b.mu.RLock()
	clients, exists := b.subscribers[auctionID]
	b.mu.RUnlock()

	if !exists {
		return
	}

	for _, c := range clients {
		go func(client *client) {
			select {
			case client.channel <- event:
			case <-time.After(1 * time.Second):
				// Channel full or blocked, skip this client
			}
		}(c)
	}
}

func (b *Broker) StreamHandler(w http.ResponseWriter, r *http.Request, auctionID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	clientID, eventChan := b.Subscribe(auctionID)
	defer b.Unsubscribe(clientID)

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"auction_id\": \"%s\"}\n\n", auctionID)
	flusher.Flush()

	// Keep-alive ticker
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case event, ok := <-eventChan:
			if !ok {
				return
			}

			data, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}

			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		}
	}
}

var globalBroker *Broker
var once sync.Once

func GetBroker() *Broker {
	once.Do(func() {
		globalBroker = NewBroker()
	})
	return globalBroker
}

func PublishBidPlaced(auctionID string, data BidPlacedData) {
	GetBroker().Publish(auctionID, Event{
		Type: "bid_placed",
		Data: data,
	})
}

func PublishTimeExtended(auctionID string, data TimeExtendedData) {
	GetBroker().Publish(auctionID, Event{
		Type: "time_extended",
		Data: data,
	})
}

func PublishAuctionEnded(auctionID string, data AuctionEndedData) {
	GetBroker().Publish(auctionID, Event{
		Type: "auction_ended",
		Data: data,
	})
}
