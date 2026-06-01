package main

import (
	"testing"

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
