package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Simple in-memory cache with TTL (can be replaced with Redis later)
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*entry
}

type entry struct {
	value     []byte
	expiresAt time.Time
}

func NewCache() *Cache {
	c := &Cache{
		entries: make(map[string]*entry),
	}
	go c.cleanup()
	return c
}

func (c *Cache) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, e := range c.entries {
			if now.After(e.expiresAt) {
				delete(c.entries, key)
			}
		}
		c.mu.Unlock()
	}
}

func (c *Cache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &entry{
		value:     data,
		expiresAt: time.Now().Add(ttl),
	}

	return nil
}

func (c *Cache) Get(ctx context.Context, key string, dest interface{}) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, exists := c.entries[key]
	if !exists {
		return fmt.Errorf("cache miss")
	}

	if time.Now().After(e.expiresAt) {
		return fmt.Errorf("cache expired")
	}

	return json.Unmarshal(e.value, dest)
}

func (c *Cache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
	return nil
}

func (c *Cache) DeletePattern(ctx context.Context, pattern string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simple pattern matching for auction:* style keys
	for key := range c.entries {
		if matchPattern(key, pattern) {
			delete(c.entries, key)
		}
	}

	return nil
}

func matchPattern(key, pattern string) bool {
	// Simple wildcard matching
	if pattern == "*" {
		return true
	}

	// Pattern like "auction:*"
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(key) >= len(prefix) && key[:len(prefix)] == prefix
	}

	return key == pattern
}

var globalCache *Cache
var cacheOnce sync.Once

func GetCache() *Cache {
	cacheOnce.Do(func() {
		globalCache = NewCache()
	})
	return globalCache
}

// Helper functions for auction caching
func CacheAuction(ctx context.Context, auctionID string, data interface{}) error {
	return GetCache().Set(ctx, fmt.Sprintf("auction:%s", auctionID), data, 5*time.Second)
}

func GetCachedAuction(ctx context.Context, auctionID string, dest interface{}) error {
	return GetCache().Get(ctx, fmt.Sprintf("auction:%s", auctionID), dest)
}

func InvalidateAuction(ctx context.Context, auctionID string) error {
	return GetCache().Delete(ctx, fmt.Sprintf("auction:%s", auctionID))
}

func InvalidateAllAuctions(ctx context.Context) error {
	return GetCache().DeletePattern(ctx, "auction:*")
}
