package ratelimit_test

import (
	"testing"
	"time"

	"bidding/internal/ratelimit"
)

func TestRateLimiter(t *testing.T) {
	rl := ratelimit.NewRateLimiter(3, 1*time.Second)

	// First 3 requests should pass
	if !rl.Allow("user1") {
		t.Fatal("first request should be allowed")
	}
	if !rl.Allow("user1") {
		t.Fatal("second request should be allowed")
	}
	if !rl.Allow("user1") {
		t.Fatal("third request should be allowed")
	}

	// 4th request should be blocked
	if rl.Allow("user1") {
		t.Fatal("fourth request should be blocked")
	}

	// Wait for window to reset
	time.Sleep(1100 * time.Millisecond)

	// Should be allowed again
	if !rl.Allow("user1") {
		t.Fatal("request after window should be allowed")
	}
}

func TestRateLimiterDifferentUsers(t *testing.T) {
	rl := ratelimit.NewRateLimiter(2, 1*time.Second)

	// user1 uses up their limit
	rl.Allow("user1")
	rl.Allow("user1")

	// user2 should still be allowed
	if !rl.Allow("user2") {
		t.Fatal("user2 should be allowed (different key)")
	}
}
