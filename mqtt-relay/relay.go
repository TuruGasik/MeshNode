package main

import (
	"fmt"
	"log/slog"
	"sync/atomic"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Stats tracks relay statistics using atomic counters (zero lock overhead).
type Stats struct {
	Received   atomic.Int64
	FromLocal  atomic.Int64
	FromUpA    atomic.Int64
	FromUpB    atomic.Int64
	RelayedIn  atomic.Int64
	RelayedOut atomic.Int64
	Dropped    atomic.Int64
}

// String returns a formatted stats summary.
func (s *Stats) String() string {
	return fmt.Sprintf(
		"received: %d | from_local: %d | from_up_a: %d | from_up_b: %d | relayed_in: %d | relayed_out: %d | dropped: %d",
		s.Received.Load(),
		s.FromLocal.Load(),
		s.FromUpA.Load(),
		s.FromUpB.Load(),
		s.RelayedIn.Load(),
		s.RelayedOut.Load(),
		s.Dropped.Load(),
	)
}

const (
	sourceLocal = "local"
	sourceUpA   = "upstream_a"
	sourceUpB   = "upstream_b"
)

type publishTarget struct {
	client mqtt.Client
	label  string
}

// Relay handles MQTT message routing and deduplication.
type Relay struct {
	config    *Config
	dedup     *DedupStore
	stats     *Stats
	onMessage func() // callback for health tracking
	local     mqtt.Client
	upA       mqtt.Client
	upB       mqtt.Client
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

// SetClients wires the connected MQTT clients into the relay.
func (r *Relay) SetClients(local, upA, upB mqtt.Client) {
	r.local = local
	r.upA = upA
	r.upB = upB
}

// HandleLocalMessage processes messages originating from the local broker.
func (r *Relay) HandleLocalMessage(_ mqtt.Client, msg mqtt.Message) {
	r.handleMessage(sourceLocal, msg)
}

// HandleUpstreamAMessage processes messages originating from upstream A.
func (r *Relay) HandleUpstreamAMessage(_ mqtt.Client, msg mqtt.Message) {
	r.handleMessage(sourceUpA, msg)
}

// HandleUpstreamBMessage processes messages originating from upstream B.
func (r *Relay) HandleUpstreamBMessage(_ mqtt.Client, msg mqtt.Message) {
	r.handleMessage(sourceUpB, msg)
}

func (r *Relay) handleMessage(source string, msg mqtt.Message) {
	topic := msg.Topic()
	payload := msg.Payload()

	r.stats.Received.Add(1)
	switch source {
	case sourceLocal:
		r.stats.FromLocal.Add(1)
	case sourceUpA:
		r.stats.FromUpA.Add(1)
	case sourceUpB:
		r.stats.FromUpB.Add(1)
	}
	if r.onMessage != nil {
		r.onMessage()
	}

	if topic == "" || !r.config.TopicMatcher(topic) {
		slog.Warn("Ignoring message on unexpected topic", "topic", topic)
		return
	}

	msgHash := Hash(topic, payload)
	seen := r.dedup.CheckAndStore(msgHash, source)
	targets, direction := r.targetsFor(source, seen)
	if len(targets) == 0 {
		r.stats.Dropped.Add(1)
		slog.Debug("DROP",
			"dir", direction,
			"topic", topic,
			"bytes", len(payload),
			"source", source,
			"previous_source", seen.PreviousSrc,
			"hash", msgHash[:12],
		)
		return
	}

	for _, target := range targets {
		token := target.client.Publish(topic, 0, false, payload)
		token.Wait()
		if err := token.Error(); err != nil {
			slog.Warn("Publish failed",
				"dir", direction,
				"topic", topic,
				"target", target.label,
				"error", err,
			)
			continue
		}
	}

	if source == sourceLocal {
		r.stats.RelayedOut.Add(int64(len(targets)))
	} else {
		r.stats.RelayedIn.Add(1)
	}

	slog.Debug("RELAY",
		"dir", direction,
		"from", source,
		"to_count", len(targets),
		"topic", topic,
		"bytes", len(payload),
		"hash", msgHash[:12],
	)
	if seen.IsNew {
		return
	}
}

func (r *Relay) targetsFor(source string, seen SeenResult) ([]publishTarget, string) {
	connected := func(client mqtt.Client) bool {
		return client != nil && client.IsConnected()
	}

	switch source {
	case sourceLocal:
		var targets []publishTarget
		if connected(r.upA) {
			targets = append(targets, publishTarget{client: r.upA, label: sourceUpA})
		}
		if connected(r.upB) {
			targets = append(targets, publishTarget{client: r.upB, label: sourceUpB})
		}
		return targets, "OUT"
	case sourceUpA, sourceUpB:
		if !connected(r.local) {
			return nil, "IN"
		}
		if !seen.IsNew {
			return nil, "IN"
		}
		return []publishTarget{{client: r.local, label: sourceLocal}}, "IN"
	default:
		return nil, "UNKNOWN"
	}
}
