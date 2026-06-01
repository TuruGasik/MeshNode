package util

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnv returns the trimmed value of the env var or fallback when unset/empty.
func GetEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// GetEnvAny returns the first non-empty env var from keys, or fallback.
func GetEnvAny(keys []string, fallback string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}

// GetEnvInt parses key as a signed int (decimal or 0x-prefixed hex).
func GetEnvInt(key string, fallback int) int {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if n, err := strconv.ParseInt(value, 0, 64); err == nil {
			return int(n)
		}
	}
	return fallback
}

// GetEnvFloat parses key as a float64.
func GetEnvFloat(key string, fallback float64) float64 {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return fallback
}

// GetEnvBool accepts 1/true/yes/y/on and 0/false/no/n/off (case-insensitive).
func GetEnvBool(key string, fallback bool) bool {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		switch strings.ToLower(value) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return fallback
}

// GetEnvDuration accepts Go duration strings (e.g. "30s", "5m") or a bare
// integer interpreted as milliseconds.
func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
		if ms, err := strconv.Atoi(value); err == nil {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return fallback
}

// GetEnvNodeList parses a comma-separated list of Meshtastic node IDs in
// hex (with or without "!" / "0x" prefix).
func GetEnvNodeList(key string, fallback []uint32) []uint32 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return append([]uint32(nil), fallback...)
	}

	parts := strings.Split(value, ",")
	out := make([]uint32, 0, len(parts))
	for _, part := range parts {
		node, ok := ParseNodeID(part)
		if ok {
			out = append(out, node)
		}
	}
	if len(out) == 0 {
		return append([]uint32(nil), fallback...)
	}
	return out
}

// ParseNodeID parses a Meshtastic node ID (hex string with optional "!" or
// "0x" prefix) into a uint32.
func ParseNodeID(value string) (uint32, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "!")
	if value == "" {
		return 0, false
	}
	if !strings.HasPrefix(strings.ToLower(value), "0x") {
		value = "0x" + value
	}
	node, err := strconv.ParseUint(value, 0, 32)
	if err != nil {
		return 0, false
	}
	return uint32(node), true
}

// LoadDotEnv loads KEY=VALUE pairs from one or more dotenv files, ignoring
// missing files and never overwriting variables already set in the
// environment.
func LoadDotEnv(paths ...string) {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if key == "" {
				continue
			}
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, value)
			}
		}
	}
}
