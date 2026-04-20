package main

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"
)

// DedupEntry stores first-seen metadata for a message hash.
type DedupEntry struct {
	Timestamp int64
	Source    string
}

// DedupStore is a thread-safe deduplication store using sync.Map.
// Keys are SHA-256 hex strings, values are DedupEntry.
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

// SeenResult returns the dedup evaluation result for a message hash.
type SeenResult struct {
	IsNew       bool
	PreviousSrc string
}

// CheckAndStore returns whether the hash is new within the TTL window and
// records the latest source for routing decisions.
func (d *DedupStore) CheckAndStore(hash, source string) SeenResult {
	now := time.Now().Unix()
	entry := DedupEntry{Timestamp: now, Source: source}

	val, loaded := d.store.LoadOrStore(hash, entry)
	if !loaded {
		return SeenResult{IsNew: true}
	}

	// Entry exists — check if it's expired
	prev := val.(DedupEntry)
	if now-prev.Timestamp >= int64(d.ttl.Seconds()) {
		// Expired entry, refresh timestamp
		d.store.Store(hash, entry)
		return SeenResult{IsNew: true, PreviousSrc: prev.Source}
	}

	return SeenResult{IsNew: false, PreviousSrc: prev.Source}
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
			entry := value.(DedupEntry)
			if now-entry.Timestamp >= ttlSec {
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
