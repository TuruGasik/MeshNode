package util

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatNodeID(t *testing.T) {
	if got := FormatNodeID(0x77727342); got != "!77727342" {
		t.Fatalf("FormatNodeID() = %q", got)
	}
}

func TestTrimRunes(t *testing.T) {
	got := TrimRunes("abcdef", 4)
	if got != "abc…" {
		t.Fatalf("TrimRunes() = %q", got)
	}

	got = TrimRunes("gempa", 10)
	if got != "gempa" {
		t.Fatalf("TrimRunes() should keep short text, got %q", got)
	}
}

func TestTruncateUTF8KeepsValidString(t *testing.T) {
	input := "Gempa 🌋 besar di laut"
	got := TruncateUTF8(input, 12)
	if !utf8.ValidString(got) {
		t.Fatalf("TruncateUTF8() returned invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("TruncateUTF8() should add ellipsis, got %q", got)
	}
	if len([]byte(got)) > 12 {
		t.Fatalf("TruncateUTF8() length = %d bytes, want <= 12", len([]byte(got)))
	}
}
