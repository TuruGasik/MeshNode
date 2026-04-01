package main

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"
)

// DedupStore is a thread-safe deduplication store using sync.Map.
// Keys are SHA-256 hex strings, values are Unix timestamps (int64).
type DedupStore struct {
	store sync.Map
	ttl   time.Duration
}

// NewDedupStore creates a new dedup store with the given TTL.
func NewDedupStore(ttl time.Duration) *DedupStore {
	return &DedupStore{
		ttl: ttl,
	}
}

// Hash computes SHA-256 of canonical topic + payload and returns hex string.
func Hash(topic string, payload []byte) string {
	h := sha256.New()
	h.Write([]byte(topic))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// IsNew returns true if the hash has not been seen within the TTL window.
// Uses LoadOrStore for atomic check-and-set to prevent race conditions
// where two goroutines could both see "not found" simultaneously.
func (d *DedupStore) IsNew(hash string) bool {
	now := time.Now().Unix()

	val, loaded := d.store.LoadOrStore(hash, now)
	if !loaded {
		return true // first time seeing this hash
	}

	// Entry exists — check if it's expired
	ts := val.(int64)
	if now-ts >= int64(d.ttl.Seconds()) {
		// Expired entry, refresh timestamp
		d.store.Store(hash, now)
		return true
	}

	return false // duplicate within TTL
}

// CleanupLoop periodically evicts expired hashes from the store.
// This should be run as a goroutine.
func (d *DedupStore) CleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()
		ttlSec := int64(d.ttl.Seconds())
		evicted := 0
		remaining := 0

		d.store.Range(func(key, value any) bool {
			ts := value.(int64)
			if now-ts >= ttlSec {
				d.store.Delete(key)
				evicted++
			} else {
				remaining++
			}
			return true
		})

		if evicted > 0 {
			slog.Debug("Cleanup: evicted expired hashes",
				"evicted", evicted,
				"remaining", remaining,
			)
		}
	}
}

// Size returns the approximate number of entries in the store.
func (d *DedupStore) Size() int {
	count := 0
	d.store.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
