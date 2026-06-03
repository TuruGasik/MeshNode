package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

// DedupEntry stores first-seen metadata for a message hash.
type DedupEntry struct {
	Timestamp int64
	Source    string
}

// DedupStore is a thread-safe deduplication store using sync.Map.
// Keys are SHA-256 hex strings, values are DedupEntry. The size counter
// avoids O(N) Range traversal when reporting cache size in /health.
type DedupStore struct {
	store sync.Map
	ttl   time.Duration
	size  atomic.Int64
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

// CanonicalHash computes a dedup hash that is stable for Meshtastic MQTT
// packets even when relay/gateway metadata changes. Some brokers/bridges echo
// the same MeshPacket with mutable fields like hop_limit, via_mqtt, or
// transport_mechanism changed/removed. Hashing the raw ServiceEnvelope bytes
// misses those duplicates, so for protobuf packets we hash only the packet
// identity and encrypted/decoded payload. Non-protobuf payloads fall back to
// the raw hash above.
func CanonicalHash(topic string, payload []byte) string {
	if canonical, ok := canonicalMeshtasticPacket(payload); ok {
		h := sha256.New()
		h.Write([]byte(topic))
		h.Write(canonical)
		return hex.EncodeToString(h.Sum(nil))
	}
	return Hash(topic, payload)
}

func canonicalMeshtasticPacket(envelope []byte) ([]byte, bool) {
	packet, ok := serviceEnvelopePacket(envelope)
	if !ok {
		return nil, false
	}

	var from, to, id, channel uint64
	var encrypted []byte
	var decoded []byte
	var haveFrom, haveTo, haveID bool

	buf := packet
	for len(buf) > 0 {
		num, typ, n := protowire.ConsumeTag(buf)
		if n < 0 {
			return nil, false
		}
		buf = buf[n:]

		switch num {
		case 1: // fixed32 from
			if typ != protowire.Fixed32Type {
				return nil, false
			}
			v, m := protowire.ConsumeFixed32(buf)
			if m < 0 {
				return nil, false
			}
			from = uint64(v)
			haveFrom = true
			buf = buf[m:]
		case 2: // fixed32 to
			if typ != protowire.Fixed32Type {
				return nil, false
			}
			v, m := protowire.ConsumeFixed32(buf)
			if m < 0 {
				return nil, false
			}
			to = uint64(v)
			haveTo = true
			buf = buf[m:]
		case 3: // uint32 channel hash
			if typ != protowire.VarintType {
				return nil, false
			}
			v, m := protowire.ConsumeVarint(buf)
			if m < 0 {
				return nil, false
			}
			channel = v
			buf = buf[m:]
		case 4: // decoded Data
			if typ != protowire.BytesType {
				return nil, false
			}
			v, m := protowire.ConsumeBytes(buf)
			if m < 0 {
				return nil, false
			}
			decoded = append(decoded[:0], v...)
			buf = buf[m:]
		case 5: // encrypted Data
			if typ != protowire.BytesType {
				return nil, false
			}
			v, m := protowire.ConsumeBytes(buf)
			if m < 0 {
				return nil, false
			}
			encrypted = append(encrypted[:0], v...)
			buf = buf[m:]
		case 6: // fixed32 packet id
			if typ != protowire.Fixed32Type {
				return nil, false
			}
			v, m := protowire.ConsumeFixed32(buf)
			if m < 0 {
				return nil, false
			}
			id = uint64(v)
			haveID = true
			buf = buf[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, buf)
			if m < 0 {
				return nil, false
			}
			buf = buf[m:]
		}
	}

	if !haveFrom || !haveTo || !haveID || (len(encrypted) == 0 && len(decoded) == 0) {
		return nil, false
	}

	// Length-prefix encrypted/decoded so two distinct paks (e.g. one with
	// encrypted=AB|decoded="" and one with encrypted=A|decoded=B) cannot
	// produce identical canonical bytes. In practice Meshtastic uses a oneof,
	// but the prefix makes the hash robust to future schema changes.
	canonical := make([]byte, 0, 8*6+len(encrypted)+len(decoded))
	canonical = appendUint64(canonical, from)
	canonical = appendUint64(canonical, to)
	canonical = appendUint64(canonical, id)
	canonical = appendUint64(canonical, channel)
	canonical = appendUint64(canonical, uint64(len(encrypted)))
	canonical = append(canonical, encrypted...)
	canonical = appendUint64(canonical, uint64(len(decoded)))
	canonical = append(canonical, decoded...)
	return canonical, true
}

func serviceEnvelopePacket(envelope []byte) ([]byte, bool) {
	buf := envelope
	for len(buf) > 0 {
		num, typ, n := protowire.ConsumeTag(buf)
		if n < 0 {
			return nil, false
		}
		buf = buf[n:]

		if num == 1 && typ == protowire.BytesType {
			packet, m := protowire.ConsumeBytes(buf)
			if m < 0 {
				return nil, false
			}
			return packet, true
		}

		m := protowire.ConsumeFieldValue(num, typ, buf)
		if m < 0 {
			return nil, false
		}
		buf = buf[m:]
	}
	return nil, false
}

func appendUint64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

// SeenResult returns the dedup evaluation result for a message hash.
type SeenResult struct {
	IsNew       bool
	PreviousSrc string
}

// CheckAndStore returns whether the hash is new within the TTL window and
// records the first-seen source for routing decisions. The PreviousSrc field
// reflects the source that originally inserted the entry (or, on TTL refresh,
// the source that owned the just-expired entry).
func (d *DedupStore) CheckAndStore(hash, source string) SeenResult {
	now := time.Now().Unix()
	ttlSec := int64(d.ttl.Seconds())
	entry := DedupEntry{Timestamp: now, Source: source}

	for {
		val, loaded := d.store.LoadOrStore(hash, entry)
		if !loaded {
			d.size.Add(1)
			return SeenResult{IsNew: true}
		}

		prev := val.(DedupEntry)
		if now-prev.Timestamp < ttlSec {
			// Still valid — duplicate.
			return SeenResult{IsNew: false, PreviousSrc: prev.Source}
		}

		// Entry expired. Atomically swap so we don't double-count when the
		// CleanupLoop deletes it in parallel, and so two concurrent refreshers
		// can't both claim IsNew=true.
		if d.store.CompareAndSwap(hash, val, entry) {
			return SeenResult{IsNew: true, PreviousSrc: prev.Source}
		}
		// Lost the race — either CleanupLoop removed the entry or another
		// goroutine already refreshed it. Retry from the top.
	}
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

		d.store.Range(func(key, value any) bool {
			entry := value.(DedupEntry)
			if now-entry.Timestamp >= ttlSec {
				if d.store.CompareAndDelete(key, entry) {
					d.size.Add(-1)
					evicted++
				}
			}
			return true
		})

		if evicted > 0 {
			slog.Debug("Cleanup: evicted expired hashes",
				"evicted", evicted,
				"remaining", d.size.Load(),
			)
		}
	}
}

// Size returns the current number of entries in the store. O(1).
func (d *DedupStore) Size() int {
	return int(d.size.Load())
}
