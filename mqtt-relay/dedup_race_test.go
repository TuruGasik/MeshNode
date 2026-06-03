package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCheckAndStoreNoDoubleCountOnExpiry exercises the race between
// CheckAndStore refreshing an expired entry and CleanupLoop deleting it.
// The Size() counter must stay consistent with the actual map contents.
func TestCheckAndStoreNoDoubleCountOnExpiry(t *testing.T) {
	d := NewDedupStore(1 * time.Second)

	// Seed a single entry, let it expire (Unix() resolution is 1s).
	d.CheckAndStore("hash-1", "local")
	time.Sleep(1100 * time.Millisecond)

	var wg sync.WaitGroup
	// One goroutine simulates the cleanup sweep, others simulate refresh.
	wg.Add(1)
	go func() {
		defer wg.Done()
		now := time.Now().Unix()
		ttlSec := int64(d.ttl.Seconds())
		d.store.Range(func(key, value any) bool {
			entry := value.(DedupEntry)
			if now-entry.Timestamp >= ttlSec {
				if d.store.CompareAndDelete(key, entry) {
					d.size.Add(-1)
				}
			}
			return true
		})
	}()

	const refreshers = 16
	newCount := atomic.Int64{}
	for i := 0; i < refreshers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := d.CheckAndStore("hash-1", "upstream_a")
			if res.IsNew {
				newCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := d.Size(); got != 1 {
		t.Fatalf("Size after concurrent refresh = %d, want 1", got)
	}
	// Walk the map to confirm Size() matches reality.
	actual := 0
	d.store.Range(func(_, _ any) bool { actual++; return true })
	if actual != 1 {
		t.Fatalf("map walk = %d, want 1 (Size reported %d)", actual, d.Size())
	}
	// At most one refresher should have been told the entry is "new". The
	// pre-fix code allowed several to all return IsNew=true, which would
	// cause the same packet to be relayed multiple times.
	if n := newCount.Load(); n > 1 {
		t.Fatalf("got %d refreshers seeing IsNew=true, want at most 1", n)
	}
}

// TestCheckAndStoreSizeInvariantUnderChurn fuzzes the store with many
// concurrent inserts/refreshes and checks Size() against a manual count.
func TestCheckAndStoreSizeInvariantUnderChurn(t *testing.T) {
	d := NewDedupStore(1 * time.Second)

	const goroutines = 32
	const perGoroutine = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				key := fmt.Sprintf("hash-%d", i%50) // collide heavily
				d.CheckAndStore(key, "local")
			}
		}(g)
	}
	wg.Wait()

	actual := 0
	d.store.Range(func(_, _ any) bool { actual++; return true })
	if got := d.Size(); got != actual {
		t.Fatalf("Size()=%d but map walk=%d", got, actual)
	}
}

// TestCheckAndStoreTTLRefresh confirms an expired entry is treated as new and
// its replacement is then deduplicated for the next TTL window.
func TestCheckAndStoreTTLRefresh(t *testing.T) {
	d := NewDedupStore(1 * time.Second)

	res := d.CheckAndStore("k", "local")
	if !res.IsNew {
		t.Fatal("first insert should be new")
	}
	res = d.CheckAndStore("k", "upstream_a")
	if res.IsNew {
		t.Fatal("immediate re-insert should be a duplicate")
	}
	if res.PreviousSrc != "local" {
		t.Fatalf("PreviousSrc=%q, want local", res.PreviousSrc)
	}

	time.Sleep(1100 * time.Millisecond)

	res = d.CheckAndStore("k", "upstream_b")
	if !res.IsNew {
		t.Fatal("post-TTL insert should be new")
	}
	if res.PreviousSrc != "local" {
		t.Fatalf("PreviousSrc on refresh=%q, want local (the just-expired source)", res.PreviousSrc)
	}
	if got := d.Size(); got != 1 {
		t.Fatalf("Size after refresh=%d, want 1", got)
	}
}
