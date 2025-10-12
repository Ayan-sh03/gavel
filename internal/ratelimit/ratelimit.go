package ratelimit

import (
	"net/http"
	"sync"
	"time"
)

type entry struct {
	count      int
	resetAt    time.Time
}

type RateLimiter struct {
	mu       sync.RWMutex
	entries  map[string]*entry
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*entry),
		limit:   limit,
		window:  window,
	}
	
	go rl.cleanup()
	
	return rl
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, e := range rl.entries {
			if now.After(e.resetAt) {
				delete(rl.entries, key)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	
	now := time.Now()
	e, exists := rl.entries[key]
	
	if !exists || now.After(e.resetAt) {
		rl.entries[key] = &entry{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}
	
	if e.count >= rl.limit {
		return false
	}
	
	e.count++
	return true
}

func (rl *RateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr
		if userID := r.Context().Value("user_id"); userID != nil {
			key = userID.(string)
		}
		
		if !rl.Allow(key) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		
		next(w, r)
	}
}
