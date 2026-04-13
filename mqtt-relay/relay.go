package main

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Stats tracks relay statistics using atomic counters (zero lock overhead).
type Stats struct {
	Received   atomic.Int64
	RelayedIn  atomic.Int64
	RelayedOut atomic.Int64
	Dropped    atomic.Int64
}

// String returns a formatted stats summary.
func (s *Stats) String() string {
	return fmt.Sprintf(
		"received: %d | relayed_in: %d | relayed_out: %d | dropped: %d",
		s.Received.Load(),
		s.RelayedIn.Load(),
		s.RelayedOut.Load(),
		s.Dropped.Load(),
	)
}

// Relay handles MQTT message routing and deduplication.
type Relay struct {
	config    *Config
	dedup     *DedupStore
	stats     *Stats
	onMessage func() // callback for health tracking
}

// NewRelay creates a new Relay instance.
func NewRelay(cfg *Config, dedup *DedupStore, onMessage func()) *Relay {
	return &Relay{
		config:    cfg,
		dedup:     dedup,
		stats:     &Stats{},
		onMessage: onMessage,
	}
}

// HandleMessage is the MQTT on_message callback.
// It determines direction (IN/OUT), computes canonical hash,
// checks dedup, and publishes to the appropriate relay topic.
//
// Flow (identical to Python relay.py):
//
//	msg received
//	  → skip if topic starts with RELAY_PREFIX (anti self-loop)
//	  → stats.Received++
//	  → determine direction:
//	      IN  = topic starts with BRIDGE_IN_PREFIX (from bridge)
//	      OUT = topic starts with SOURCE_PREFIX (from local client)
//	  → compute canonical topic (strip bridge_in/ for IN direction)
//	  → hash = SHA256(canonical + payload)
//	  → if dedup.IsNew(hash):
//	      IN  → publish to canonical topic (clean, for local clients)
//	      OUT → publish to RELAY_PREFIX + subtopic (for bridge outbound)
//	  → else:
//	      drop (duplicate)
func (r *Relay) HandleMessage(client mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	payload := msg.Payload()

	// Skip messages on the relay prefix to prevent self-loop on outbound
	if strings.HasPrefix(topic, r.config.RelayPrefix) {
		return
	}

	r.stats.Received.Add(1)
	if r.onMessage != nil {
		r.onMessage()
	}

	// ---------------------------------------------------------------
	// Determine direction and canonical topic
	// ---------------------------------------------------------------
	var canonical string
	var direction string

	if strings.HasPrefix(topic, r.config.BridgeInPrefix) {
		// INBOUND: from bridge (msh/bridge_in/ID/...) → clean topic
		// Canonical: strip bridge_in/ → msh/ID/...
		canonical = r.config.SourcePrefix + topic[len(r.config.BridgeInPrefix):]
		direction = "IN"
	} else if strings.HasPrefix(topic, r.config.SourcePrefix) {
		// OUTBOUND: from local client on msh/ID/... OR relay's own
		// inbound publish (self-echo). Dedup handles both:
		//   - Local client msg: hash is new → relay to msh/relay/...
		//   - Self-echo: hash already cached from INBOUND → dropped
		canonical = topic // already in canonical form (msh/ID/...)
		direction = "OUT"
	} else {
		slog.Warn("Ignoring message on unexpected topic", "topic", topic)
		return
	}

	// ---------------------------------------------------------------
	// Dedup using canonical hash
	// ---------------------------------------------------------------
	msgHash := Hash(canonical, payload)

	if r.dedup.IsNew(msgHash) {
		var relayTopic string

		if direction == "IN" {
			// Bridge inbound → publish to clean topic for local clients
			relayTopic = canonical
			r.stats.RelayedIn.Add(1)
		} else {
			// Local client → publish to relay topic for bridge outbound
			relayTopic = r.config.RelayPrefix + topic[len(r.config.SourcePrefix):]
			r.stats.RelayedOut.Add(1)
		}

		client.Publish(relayTopic, 0, false, payload)

		slog.Debug("RELAY",
			"dir", direction,
			"from", topic,
			"to", relayTopic,
			"bytes", len(payload),
			"hash", msgHash[:12],
		)
	} else {
		r.stats.Dropped.Add(1)

		slog.Debug("DROP",
			"dir", direction,
			"topic", topic,
			"bytes", len(payload),
			"hash", msgHash[:12],
		)
	}
}
