package util

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

var randomFallbackCounter uint64

func FormatNodeID(node uint32) string {
	return fmt.Sprintf("!%08x", node)
}

func TruncateUTF8(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	encoded := []byte(text)
	if len(encoded) <= maxBytes {
		return text
	}
	if maxBytes <= 3 {
		return "..."[:maxBytes]
	}
	trimmed := string(encoded[:maxBytes-3])
	for !utf8.ValidString(trimmed) && len(trimmed) > 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return strings.TrimRight(trimmed, " \t\r\n") + "..."
}

func RandomUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return binary.LittleEndian.Uint32(b[:])
	}
	counter := atomic.AddUint64(&randomFallbackCounter, 1)
	h := fnv.New32a()
	var seed [16]byte
	binary.LittleEndian.PutUint64(seed[0:], uint64(time.Now().UnixNano()))
	binary.LittleEndian.PutUint64(seed[8:], counter)
	_, _ = h.Write(seed[:])
	return h.Sum32()
}

func TrimRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
