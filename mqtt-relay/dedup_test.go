package main

import (
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestCanonicalHashIgnoresMutableHopMetadata(t *testing.T) {
	topic := "msh/ID/2/e/GempaBumi/!a707e420"
	payload := []byte{0x01, 0x02, 0x03, 0x04}
	full := testEnvelope(testPacket(payload, true))
	mutated := testEnvelope(testPacket(payload, false))

	if Hash(topic, full) == Hash(topic, mutated) {
		t.Fatal("raw hashes unexpectedly equal")
	}
	if CanonicalHash(topic, full) != CanonicalHash(topic, mutated) {
		t.Fatal("canonical hashes should match when only hop/MQTT metadata differs")
	}
}

func TestForgetAllowsRetryAfterFailedDelivery(t *testing.T) {
	d := NewDedupStore(10 * time.Minute)

	first := d.CheckAndStore("hash_X", "local")
	if !first.IsNew {
		t.Fatal("first CheckAndStore should be new")
	}
	if d.Size() != 1 {
		t.Fatalf("size = %d, want 1", d.Size())
	}

	// Delivery failed everywhere → roll back the insertion.
	d.Forget("hash_X", first.Entry)
	if d.Size() != 0 {
		t.Fatalf("size after Forget = %d, want 0", d.Size())
	}

	// A retransmission must now pass as new instead of being dropped.
	if retry := d.CheckAndStore("hash_X", "local"); !retry.IsNew {
		t.Fatal("retry after Forget should be new")
	}
}

func TestForgetIgnoresNonMatchingEntry(t *testing.T) {
	d := NewDedupStore(10 * time.Minute)

	first := d.CheckAndStore("hash_X", "local")
	if !first.IsNew {
		t.Fatal("first CheckAndStore should be new")
	}

	// A stale token (e.g. from an entry that was since refreshed by another
	// goroutine) must not evict the current entry.
	stale := first.Entry
	stale.Source = "upstream_a"
	d.Forget("hash_X", stale)

	if d.Size() != 1 {
		t.Fatalf("size = %d, want 1 (stale Forget must be a no-op)", d.Size())
	}
	if dup := d.CheckAndStore("hash_X", "upstream_a"); dup.IsNew {
		t.Fatal("entry should still be present after stale Forget")
	}
}

func testEnvelope(packet []byte) []byte {
	var out []byte
	out = protowire.AppendTag(out, protowire.Number(1), protowire.BytesType)
	out = protowire.AppendBytes(out, packet)
	return out
}

func testPacket(encrypted []byte, fullMetadata bool) []byte {
	var out []byte
	out = appendFixed32Field(out, protowire.Number(1), 0xa707e420)
	out = appendFixed32Field(out, protowire.Number(2), 0xffffffff)
	out = appendVarintField(out, protowire.Number(3), 111)
	out = appendBytesField(out, protowire.Number(5), encrypted)
	out = appendFixed32Field(out, protowire.Number(6), 0x8a3e107c)
	if fullMetadata {
		out = appendVarintField(out, protowire.Number(9), 4)
		out = appendVarintField(out, protowire.Number(14), 0)
		out = appendVarintField(out, protowire.Number(21), 0)
	}
	out = appendVarintField(out, protowire.Number(15), 4)
	return out
}

func appendFixed32Field(out []byte, num protowire.Number, value uint32) []byte {
	out = protowire.AppendTag(out, num, protowire.Fixed32Type)
	return protowire.AppendFixed32(out, value)
}

func appendVarintField(out []byte, num protowire.Number, value uint64) []byte {
	out = protowire.AppendTag(out, num, protowire.VarintType)
	return protowire.AppendVarint(out, value)
}

func appendBytesField(out []byte, num protowire.Number, value []byte) []byte {
	out = protowire.AppendTag(out, num, protowire.BytesType)
	return protowire.AppendBytes(out, value)
}
