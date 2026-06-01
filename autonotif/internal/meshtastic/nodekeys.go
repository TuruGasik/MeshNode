package meshtastic

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"meshnode/autonotif/internal/util"
)

// nodeKeyStore is a thread-safe in-memory cache of remote node PKI public keys
// with optional JSON file persistence. Each remembered key is written back to
// disk so that reconnects can immediately use end-to-end encryption.
type nodeKeyStore struct {
	mu   sync.RWMutex
	keys map[uint32][]byte
	file string
}

func newNodeKeyStore(file string) *nodeKeyStore {
	s := &nodeKeyStore{
		keys: make(map[uint32][]byte),
		file: file,
	}
	s.loadFromFile()
	return s
}

// Remember caches publicKey for node and persists the cache. A no-op if the
// key is already cached unchanged.
func (s *nodeKeyStore) Remember(node uint32, publicKey []byte) {
	if node == 0 || len(publicKey) != pkiKeySize {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.keys[node]
	if exists && bytes.Equal(previous, publicKey) {
		return
	}
	s.keys[node] = append([]byte(nil), publicKey...)
	slog.Info("cached node pki public key", "node", util.FormatNodeID(node))
	s.saveLocked()
}

// Lookup returns a copy of the cached public key, or false when missing.
func (s *nodeKeyStore) Lookup(node uint32) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := s.keys[node]
	if !ok || len(key) != pkiKeySize {
		return nil, false
	}
	return append([]byte(nil), key...), true
}

func (s *nodeKeyStore) loadFromFile() {
	if s.file == "" {
		return
	}
	data, err := os.ReadFile(s.file)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("node pki key cache could not be loaded", "file", s.file, "error", err)
		}
		return
	}
	var encoded map[string]string
	if err := json.Unmarshal(data, &encoded); err != nil {
		slog.Warn("node pki key cache could not be decoded", "file", s.file, "error", err)
		return
	}
	loaded := 0
	for nodeID, keyB64 := range encoded {
		node, ok := parseNodeID(nodeID)
		if !ok {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(keyB64)
		if err != nil || len(key) != pkiKeySize {
			continue
		}
		s.keys[node] = append([]byte(nil), key...)
		loaded++
	}
	if loaded > 0 {
		slog.Info("loaded node pki key cache", "file", s.file, "nodes", loaded)
	}
}

func (s *nodeKeyStore) saveLocked() {
	if s.file == "" {
		return
	}
	encoded := make(map[string]string, len(s.keys))
	for node, key := range s.keys {
		if len(key) == pkiKeySize {
			encoded[util.FormatNodeID(node)] = base64.StdEncoding.EncodeToString(key)
		}
	}
	data, err := json.MarshalIndent(encoded, "", "  ")
	if err != nil {
		slog.Warn("node pki key cache could not be encoded", "file", s.file, "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		slog.Warn("node pki key cache directory could not be created", "file", s.file, "error", err)
		return
	}
	if err := os.WriteFile(s.file, append(data, '\n'), 0o600); err != nil {
		slog.Warn("node pki key cache could not be saved", "file", s.file, "error", err)
	}
}

func parseNodeID(value string) (uint32, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "!")
	value = strings.TrimPrefix(value, "0x")
	value = strings.TrimPrefix(value, "0X")
	if value == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}
