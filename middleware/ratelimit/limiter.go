// Package ratelimit provides rate limiting middleware for the revelt framework.
package ratelimit

import (
	"sync"
	"time"
)

// tokenBucket implements the token bucket rate-limiting algorithm.
// Safe for concurrent use by multiple goroutines.
type tokenBucket struct {
	rate       float64    // tokens added per second
	capacity   float64    // maximum tokens the bucket can hold
	tokens     float64    // current token count
	lastRefill time.Time  // timestamp of the last refill
	mu         sync.Mutex // protects tokens and lastRefill
}

// newTokenBucket creates a new token bucket starting at full capacity.
func newTokenBucket(rate, capacity float64) *tokenBucket {
	return &tokenBucket{
		rate:       rate,
		capacity:   capacity,
		tokens:     capacity,
		lastRefill: time.Now(),
	}
}

// allow reports whether one token can be consumed, refilling the bucket
// based on elapsed time since the last check. Safe for concurrent use.
func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()

	tokensToAdd := elapsed * tb.rate
	tb.tokens += tokensToAdd
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.lastRefill = now

	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}

// Manager manages multiple token buckets, keyed by an arbitrary string
// (e.g. client IP or API key). Safe for concurrent use by multiple
// goroutines.
type Manager struct {
	buckets    map[string]*tokenBucket
	mu         sync.RWMutex
	rate       float64
	capacity   float64
	expiration time.Duration // idle-bucket eviction interval
}

// NewManager creates a rate limiter manager. rate is tokens per second,
// capacity is the maximum burst size, and expiration controls how long an
// idle bucket is retained in memory before being evicted by the background
// cleanup goroutine.
func NewManager(rate, capacity float64, expiration time.Duration) *Manager {
	m := &Manager{
		buckets:    make(map[string]*tokenBucket),
		rate:       rate,
		capacity:   capacity,
		expiration: expiration,
	}

	go m.cleanupLoop()
	return m
}

// Allow reports whether a request identified by key is permitted under the
// current rate limit, creating a new bucket for previously unseen keys.
// Safe for concurrent use by multiple goroutines.
func (m *Manager) Allow(key string) bool {
	m.mu.RLock()
	bucket, exists := m.buckets[key]
	m.mu.RUnlock()

	if !exists {
		m.mu.Lock()
		// Double-check under the write lock in case another goroutine
		// created the bucket between the RUnlock above and this Lock.
		bucket, exists = m.buckets[key]
		if !exists {
			bucket = newTokenBucket(m.rate, m.capacity)
			m.buckets[key] = bucket
		}
		m.mu.Unlock()
	}

	return bucket.allow()
}

// cleanupLoop periodically evicts idle buckets. Runs for the lifetime of
// the Manager; there is no explicit stop mechanism since Manager instances
// are expected to live for the lifetime of the process.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(m.expiration)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanup()
	}
}

// cleanup removes buckets that have not been refilled (i.e. checked via
// Allow) within the expiration window.
func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for key, bucket := range m.buckets {
		bucket.mu.Lock()
		if now.Sub(bucket.lastRefill) > m.expiration {
			delete(m.buckets, key)
		}
		bucket.mu.Unlock()
	}
}
