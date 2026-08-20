package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Config holds all configuration loaded from environment variables.
type Config struct {
	LocalBrokerHost             string
	LocalBrokerPort             int
	LocalBrokerTLS              bool
	LocalBrokerTLSServerName    string
	LocalBrokerUsername         string
	LocalBrokerPassword         string
	UpstreamABrokerHost         string
	UpstreamABrokerPort         int
	UpstreamABrokerUser         string
	UpstreamABrokerPass         string
	UpstreamABrokerTLS          bool
	UpstreamATLSServerName      string
	UpstreamAFallbackHosts      []string // additional hosts to try in order on failover
	UpstreamAFailoverTimeoutSec int      // seconds before rotating to next host
	UpstreamBBrokerHost         string
	UpstreamBBrokerPort         int
	UpstreamBBrokerUser         string
	UpstreamBBrokerPass         string
	UpstreamBBrokerTLS          bool
	UpstreamBTLSServerName      string
	TopicRoot                   string
	DedupTTL                    int
	CleanupInterval             int
	StatsInterval               int
	PublishQoS                  int
	SubscribeQoS                int
	PublishTimeout              int // milliseconds
	HealthPort                  int
	ShutdownTimeoutMS           int
	LogLevel                    string
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig() *Config {
	return &Config{
		LocalBrokerHost:             getEnv("LOCAL_MQTT_HOST", "meshnode-mqtt"),
		LocalBrokerPort:             getEnvInt("LOCAL_MQTT_PORT", 1883),
		LocalBrokerTLS:              getEnvBool("LOCAL_MQTT_TLS", false),
		LocalBrokerTLSServerName:    getEnvAny([]string{"LOCAL_MQTT_TLS_SERVER_NAME", "MQTT_TLS_SERVER_NAME"}, ""),
		LocalBrokerUsername:         getEnv("LOCAL_MQTT_USERNAME", ""),
		LocalBrokerPassword:         getEnv("LOCAL_MQTT_PASSWORD", ""),
		UpstreamABrokerHost:         getEnv("UPSTREAM_A_HOST", ""),
		UpstreamABrokerPort:         getEnvInt("UPSTREAM_A_PORT", 1883),
		UpstreamABrokerUser:         getEnv("UPSTREAM_A_USERNAME", ""),
		UpstreamABrokerPass:         getEnv("UPSTREAM_A_PASSWORD", ""),
		UpstreamABrokerTLS:          getEnvBool("UPSTREAM_A_TLS", false),
		UpstreamATLSServerName:      getEnv("UPSTREAM_A_TLS_SERVER_NAME", ""),
		UpstreamAFallbackHosts:      getEnvStringSlice("UPSTREAM_A_FALLBACK_HOSTS"),
		UpstreamAFailoverTimeoutSec: getEnvInt("UPSTREAM_A_FAILOVER_TIMEOUT", 30),
		UpstreamBBrokerHost:         getEnv("UPSTREAM_B_HOST", ""),
		UpstreamBBrokerPort:         getEnvInt("UPSTREAM_B_PORT", 1883),
		UpstreamBBrokerUser:         getEnv("UPSTREAM_B_USERNAME", ""),
		UpstreamBBrokerPass:         getEnv("UPSTREAM_B_PASSWORD", ""),
		UpstreamBBrokerTLS:          getEnvBool("UPSTREAM_B_TLS", false),
		UpstreamBTLSServerName:      getEnv("UPSTREAM_B_TLS_SERVER_NAME", ""),
		TopicRoot:                   getEnv("TOPIC_ROOT", "msh/ID/#"),
		DedupTTL:                    getEnvInt("DEDUP_TTL", 600),
		CleanupInterval:             getEnvInt("CLEANUP_INTERVAL", 60),
		StatsInterval:               getEnvInt("STATS_INTERVAL", 60),
		PublishQoS:                  getEnvInt("PUBLISH_QOS", 0),
		SubscribeQoS:                getEnvInt("SUBSCRIBE_QOS", 0),
		PublishTimeout:              getEnvInt("PUBLISH_TIMEOUT_MS", 5000),
		HealthPort:                  getEnvInt("HEALTH_PORT", 8081),
		ShutdownTimeoutMS:           getEnvInt("SHUTDOWN_TIMEOUT_MS", 5000),
		LogLevel:                    getEnv("LOG_LEVEL", "INFO"),
	}
}

func (c *Config) TopicMatcher(topic string) bool {
	return matchMQTTTopic(c.TopicRoot, topic)
}

// matchMQTTTopic implements MQTT topic filter matching with both '+' (single
// level) and '#' (multi level) wildcards. The filter must follow MQTT rules:
// '#' may only appear as the last level, '+' must occupy a whole level.
func matchMQTTTopic(filter, topic string) bool {
	if filter == topic {
		return true
	}
	fParts := strings.Split(filter, "/")
	tParts := strings.Split(topic, "/")
	for i, fp := range fParts {
		if fp == "#" {
			// '#' matches the rest, but must be the final segment.
			return i == len(fParts)-1
		}
		if i >= len(tParts) {
			return false
		}
		if fp == "+" {
			continue
		}
		if fp != tParts[i] {
			return false
		}
	}
	return len(fParts) == len(tParts)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvAny(keys []string, fallback string) string {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return fallback
}

// getEnvStringSlice parses a comma-separated env var into a slice, trimming
// whitespace and dropping empty entries.
func getEnvStringSlice(key string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// UpstreamInfo holds per-upstream configuration and client reference.
type UpstreamInfo struct {
	Label  string
	Host   string
	Client mqtt.Client
}

// HealthState tracks the health of the relay service.
type HealthState struct {
	connected   atomic.Bool
	lastMsgAt   atomic.Int64 // unix timestamp
	startedAt   time.Time
	stats       *Stats
	dedupSize   func() int
	upstreamsMu sync.RWMutex
	upstreams   []UpstreamInfo
}

// NewHealthState creates a new HealthState tracker.
func NewHealthState(stats *Stats, dedupSize func() int) *HealthState {
	return &HealthState{
		stats:     stats,
		dedupSize: dedupSize,
		startedAt: time.Now(),
	}
}

// SetUpstreams registers upstream clients for health tracking.
func (h *HealthState) SetUpstreams(upstreams []UpstreamInfo) {
	h.upstreamsMu.Lock()
	defer h.upstreamsMu.Unlock()
	h.upstreams = upstreams
}

// UpdateUpstreamClient replaces the host+client for the named upstream entry.
// Called by the failover goroutine when switching to a new host.
func (h *HealthState) UpdateUpstreamClient(label, host string, client mqtt.Client) {
	h.upstreamsMu.Lock()
	defer h.upstreamsMu.Unlock()
	for i, u := range h.upstreams {
		if u.Label == label {
			h.upstreams[i].Host = host
			h.upstreams[i].Client = client
			return
		}
	}
}

// SetConnected updates the connection status.
func (h *HealthState) SetConnected(connected bool) {
	h.connected.Store(connected)
}

// Touch updates the last message received time.
func (h *HealthState) Touch() {
	h.lastMsgAt.Store(time.Now().Unix())
}

// Status returns the current health status.
func (h *HealthState) Status() map[string]any {
	connected := h.connected.Load()
	lastMsgUnix := h.lastMsgAt.Load()
	uptime := time.Since(h.startedAt)

	// Determine health status
	var status string
	var reason string
	if !connected {
		status = "unhealthy"
		reason = "MQTT disconnected"
	} else if lastMsgUnix > 0 && time.Since(time.Unix(lastMsgUnix, 0)) > 10*time.Minute {
		status = "degraded"
		reason = "No messages received in 10 minutes"
	} else {
		status = "healthy"
	}

	// Build per-upstream status map
	h.upstreamsMu.RLock()
	upstreams := make([]UpstreamInfo, len(h.upstreams))
	copy(upstreams, h.upstreams)
	h.upstreamsMu.RUnlock()

	upstreamsStatus := make(map[string]any, len(upstreams))
	for _, u := range upstreams {
		configured := u.Host != ""
		isConnected := u.Client != nil && u.Client.IsConnected()
		upstreamsStatus[u.Label] = map[string]any{
			"configured": configured,
			"connected":  isConnected,
			"host":       u.Host,
		}
	}

	return map[string]any{
		"status":         status,
		"reason":         reason,
		"mqtt_connected": connected,
		"uptime_seconds": int(uptime.Seconds()),
		"last_message_at": func() string {
			if lastMsgUnix == 0 {
				return ""
			}
			return time.Unix(lastMsgUnix, 0).UTC().Format(time.RFC3339)
		}(),
		"upstreams": upstreamsStatus,
		"stats": map[string]int64{
			"received":    h.stats.Received.Load(),
			"from_local":  h.stats.FromLocal.Load(),
			"from_up_a":   h.stats.FromUpA.Load(),
			"from_up_b":   h.stats.FromUpB.Load(),
			"relayed_in":  h.stats.RelayedIn.Load(),
			"relayed_out": h.stats.RelayedOut.Load(),
			"dropped":     h.stats.Dropped.Load(),
			"cache_size":  int64(h.dedupSize()),
		},
	}
}

// startHealthServer starts an HTTP server exposing /health and /metrics endpoints.
func startHealthServer(health *HealthState, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		data := health.Status()
		status := data["status"].(string)
		w.Header().Set("Content-Type", "application/json")
		if status == "healthy" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(data)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetrics(w, health)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" && r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
	})

	go func() {
		slog.Info("Health server listening", "addr", addr, "endpoints", "/health,/metrics")
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("Health server failed", "error", err)
		}
	}()
}

// writeMetrics renders relay metrics in Prometheus text exposition format.
// No external dependencies — plain stdlib output.
func writeMetrics(w io.Writer, h *HealthState) {
	s := h.stats
	connected := int64(0)
	if h.connected.Load() {
		connected = 1
	}
	uptime := int64(time.Since(h.startedAt).Seconds())
	lastMsg := h.lastMsgAt.Load()

	fmt.Fprintln(w, "# HELP mqtt_relay_up Local broker connection state (1=connected, 0=disconnected).")
	fmt.Fprintln(w, "# TYPE mqtt_relay_up gauge")
	fmt.Fprintf(w, "mqtt_relay_up %d\n", connected)

	fmt.Fprintln(w, "# HELP mqtt_relay_uptime_seconds Process uptime in seconds.")
	fmt.Fprintln(w, "# TYPE mqtt_relay_uptime_seconds counter")
	fmt.Fprintf(w, "mqtt_relay_uptime_seconds %d\n", uptime)

	fmt.Fprintln(w, "# HELP mqtt_relay_last_message_timestamp_seconds Unix timestamp of last received message.")
	fmt.Fprintln(w, "# TYPE mqtt_relay_last_message_timestamp_seconds gauge")
	fmt.Fprintf(w, "mqtt_relay_last_message_timestamp_seconds %d\n", lastMsg)

	fmt.Fprintln(w, "# HELP mqtt_relay_messages_received_total Total messages received per source.")
	fmt.Fprintln(w, "# TYPE mqtt_relay_messages_received_total counter")
	fmt.Fprintf(w, "mqtt_relay_messages_received_total{source=\"local\"} %d\n", s.FromLocal.Load())
	fmt.Fprintf(w, "mqtt_relay_messages_received_total{source=\"upstream_a\"} %d\n", s.FromUpA.Load())
	fmt.Fprintf(w, "mqtt_relay_messages_received_total{source=\"upstream_b\"} %d\n", s.FromUpB.Load())

	fmt.Fprintln(w, "# HELP mqtt_relay_messages_relayed_total Total messages relayed by direction.")
	fmt.Fprintln(w, "# TYPE mqtt_relay_messages_relayed_total counter")
	fmt.Fprintf(w, "mqtt_relay_messages_relayed_total{direction=\"in\"} %d\n", s.RelayedIn.Load())
	fmt.Fprintf(w, "mqtt_relay_messages_relayed_total{direction=\"out\"} %d\n", s.RelayedOut.Load())

	fmt.Fprintln(w, "# HELP mqtt_relay_messages_dropped_total Total messages dropped (duplicates / loop suppression).")
	fmt.Fprintln(w, "# TYPE mqtt_relay_messages_dropped_total counter")
	fmt.Fprintf(w, "mqtt_relay_messages_dropped_total %d\n", s.Dropped.Load())

	fmt.Fprintln(w, "# HELP mqtt_relay_dedup_cache_size Current number of entries in the dedup cache.")
	fmt.Fprintln(w, "# TYPE mqtt_relay_dedup_cache_size gauge")
	fmt.Fprintf(w, "mqtt_relay_dedup_cache_size %d\n", h.dedupSize())

	fmt.Fprintln(w, "# HELP mqtt_relay_upstream_connected Upstream connection state per label (1=connected).")
	fmt.Fprintln(w, "# TYPE mqtt_relay_upstream_connected gauge")
	h.upstreamsMu.RLock()
	upstreams := make([]UpstreamInfo, len(h.upstreams))
	copy(upstreams, h.upstreams)
	h.upstreamsMu.RUnlock()
	for _, u := range upstreams {
		v := int64(0)
		if u.Client != nil && u.Client.IsConnected() {
			v = 1
		}
		fmt.Fprintf(w, "mqtt_relay_upstream_connected{label=%q,host=%q} %d\n", u.Label, u.Host, v)
	}
}

// parseSlogLevel converts a log level string to slog.Level.
func parseSlogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// runUpstreamAFailover monitors the upstream-A connection and rotates to the
// next host in allHosts when the current one stays disconnected for longer
// than cfg.UpstreamAFailoverTimeoutSec seconds.
//
// allHosts is [primary, fallback1, fallback2, …]. The function cycles through
// them in order (wrapping around) and never stops unless ctx is cancelled.
func runUpstreamAFailover(
	ctx context.Context,
	cfg *Config,
	allHosts []string,
	relay *Relay,
	health *HealthState,
	makeUpstreamA func(name, broker string) mqtt.Client,
) {
	if len(allHosts) < 2 {
		// Nothing to fail over to.
		return
	}

	failoverTimeout := time.Duration(cfg.UpstreamAFailoverTimeoutSec) * time.Second
	checkInterval := 10 * time.Second
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	currentIdx := 0
	var disconnectedSince time.Time

	schemeFor := func() string {
		if cfg.UpstreamABrokerTLS {
			return "ssl"
		}
		return "tcp"
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			client := relay.getUpA()
			if client != nil && client.IsConnected() {
				disconnectedSince = time.Time{} // reset
				continue
			}

			now := time.Now()
			if disconnectedSince.IsZero() {
				disconnectedSince = now
				slog.Warn("Upstream A disconnected, failover timer started",
					"host", allHosts[currentIdx],
					"failover_in", failoverTimeout,
				)
				continue
			}

			if time.Since(disconnectedSince) < failoverTimeout {
				continue // still within grace period, paho may reconnect on its own
			}

			// Grace period expired — rotate to the next host.
			nextIdx := (currentIdx + 1) % len(allHosts)
			nextHost := allHosts[nextIdx]
			brokerURL := fmt.Sprintf("%s://%s:%d", schemeFor(), nextHost, cfg.UpstreamABrokerPort)

			slog.Warn("Upstream A failover: switching host",
				"from", allHosts[currentIdx],
				"to", nextHost,
				"disconnected_for", time.Since(disconnectedSince).Round(time.Second),
			)

			// Disconnect the old client so it stops retry loops.
			old := relay.SwapUpstreamA(nil) // nil so relay stops routing to it immediately
			if old != nil {
				old.Disconnect(500)
			}

			// Connect to the new host.
			newClient := makeUpstreamA(fmt.Sprintf("mqtt-relay-upstream-a-%d", nextIdx), brokerURL)
			relay.SwapUpstreamA(newClient)
			health.UpdateUpstreamClient(
				"upstream_a",
				fmt.Sprintf("%s:%d", nextHost, cfg.UpstreamABrokerPort),
				newClient,
			)

			currentIdx = nextIdx
			disconnectedSince = time.Time{}
		}
	}
}

func main() {
	cfg := LoadConfig()

	// Setup structured logging
	logLevel := parseSlogLevel(cfg.LogLevel)
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))

	// Startup banner
	slog.Info("============================================================")
	slog.Info("MeshNode MQTT Relay — Deduplication Service (Go)")
	slog.Info("============================================================")
	slog.Info("Configuration",
		"local", fmt.Sprintf("%s:%d", cfg.LocalBrokerHost, cfg.LocalBrokerPort),
		"upstream_a", fmt.Sprintf("%s:%d", cfg.UpstreamABrokerHost, cfg.UpstreamABrokerPort),
		"upstream_a_fallbacks", cfg.UpstreamAFallbackHosts,
		"upstream_a_failover_timeout", fmt.Sprintf("%ds", cfg.UpstreamAFailoverTimeoutSec),
		"upstream_b", fmt.Sprintf("%s:%d", cfg.UpstreamBBrokerHost, cfg.UpstreamBBrokerPort),
		"topic_root", cfg.TopicRoot,
		"dedup_ttl", fmt.Sprintf("%ds", cfg.DedupTTL),
		"cleanup_interval", fmt.Sprintf("%ds", cfg.CleanupInterval),
		"log_level", cfg.LogLevel,
	)
	slog.Info("============================================================")

	// Initialize dedup store
	dedup := NewDedupStore(time.Duration(cfg.DedupTTL) * time.Second)

	// Initialize relay first so health.stats is wired before the HTTP server starts.
	health := NewHealthState(nil, dedup.Size)
	relay := NewRelay(cfg, dedup, health.Touch)
	relay.publishQoS = byte(cfg.PublishQoS)
	relay.publishTimeout = time.Duration(cfg.PublishTimeout) * time.Millisecond
	health.stats = relay.stats

	// HTTP health server (now safe — health.stats is set).
	startHealthServer(health, fmt.Sprintf(":%d", cfg.HealthPort))

	// Start cleanup goroutine
	go dedup.CleanupLoop(time.Duration(cfg.CleanupInterval) * time.Second)

	makeClient := func(name, broker, username, password string, useTLS bool, tlsServerName string, isLocal bool, handler mqtt.MessageHandler) mqtt.Client {
		// Add up to ±2s of jitter to the retry interval so that multiple
		// instances restarting together don't all reconnect on the same tick.
		jitter := time.Duration(rand.Intn(2000)) * time.Millisecond
		retryInterval := 5*time.Second + jitter
		opts := mqtt.NewClientOptions().
			AddBroker(broker).
			SetClientID(name).
			SetUsername(username).
			SetPassword(password).
			SetCleanSession(true).
			SetAutoReconnect(true).
			SetOrderMatters(false).
			SetConnectRetry(true).
			SetConnectRetryInterval(retryInterval).
			SetMaxReconnectInterval(30 * time.Second).
			SetKeepAlive(60 * time.Second).
			SetDefaultPublishHandler(handler).
			SetOnConnectHandler(func(client mqtt.Client) {
				slog.Info("Connected to MQTT broker", "name", name, "broker", broker)
				if token := client.Subscribe(cfg.TopicRoot, byte(cfg.SubscribeQoS), nil); token.Wait() && token.Error() != nil {
					slog.Error("Failed to subscribe",
						"name", name,
						"topic", cfg.TopicRoot,
						"error", token.Error(),
					)
				} else {
					slog.Info("Subscribed", "name", name, "topic", cfg.TopicRoot, "qos", cfg.SubscribeQoS)
				}
				if isLocal {
					health.SetConnected(true)
				}
			}).
			SetConnectionLostHandler(func(client mqtt.Client, err error) {
				slog.Warn("Connection lost, will auto-reconnect…", "name", name, "error", err)
				if isLocal {
					health.SetConnected(false)
				}
			}).
			SetReconnectingHandler(func(client mqtt.Client, opts *mqtt.ClientOptions) {
				slog.Debug("Reconnecting to MQTT broker…", "name", name)
			})
		if useTLS {
			opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: tlsServerName})
		}

		client := mqtt.NewClient(opts)
		token := client.Connect()
		token.Wait()
		if err := token.Error(); err != nil {
			if isLocal {
				slog.Error("Failed to connect to local MQTT broker, exiting", "name", name, "error", err)
				os.Exit(1)
			}
			// Upstream is non-fatal: SetConnectRetry(true) will keep retrying
			// in the background and OnConnectHandler will subscribe once up.
			slog.Warn("Initial connect failed, will keep retrying in background",
				"name", name, "error", err,
			)
		}
		return client
	}

	scheme := func(useTLS bool) string {
		if useTLS {
			return "ssl"
		}
		return "tcp"
	}
	localBroker := fmt.Sprintf("%s://%s:%d", scheme(cfg.LocalBrokerTLS), cfg.LocalBrokerHost, cfg.LocalBrokerPort)
	upstreamABroker := fmt.Sprintf("%s://%s:%d", scheme(cfg.UpstreamABrokerTLS), cfg.UpstreamABrokerHost, cfg.UpstreamABrokerPort)
	upstreamBBroker := fmt.Sprintf("%s://%s:%d", scheme(cfg.UpstreamBBrokerTLS), cfg.UpstreamBBrokerHost, cfg.UpstreamBBrokerPort)

	localClient := makeClient("mqtt-relay-local", localBroker, cfg.LocalBrokerUsername, cfg.LocalBrokerPassword, cfg.LocalBrokerTLS, cfg.LocalBrokerTLSServerName, true, relay.HandleLocalMessage)

	var upstreamAClient mqtt.Client
	if cfg.UpstreamABrokerHost != "" {
		upstreamAClient = makeClient("mqtt-relay-upstream-a", upstreamABroker, cfg.UpstreamABrokerUser, cfg.UpstreamABrokerPass, cfg.UpstreamABrokerTLS, cfg.UpstreamATLSServerName, false, relay.HandleUpstreamAMessage)
	} else {
		slog.Info("Upstream A not configured, skipping")
	}

	var upstreamBClient mqtt.Client
	if cfg.UpstreamBBrokerHost != "" {
		upstreamBClient = makeClient("mqtt-relay-upstream-b", upstreamBBroker, cfg.UpstreamBBrokerUser, cfg.UpstreamBBrokerPass, cfg.UpstreamBBrokerTLS, cfg.UpstreamBTLSServerName, false, relay.HandleUpstreamBMessage)
	} else {
		slog.Info("Upstream B not configured, skipping")
	}

	relay.SetClients(localClient, upstreamAClient, upstreamBClient)
	upstreamAAddr := ""
	if cfg.UpstreamABrokerHost != "" {
		upstreamAAddr = fmt.Sprintf("%s:%d", cfg.UpstreamABrokerHost, cfg.UpstreamABrokerPort)
	}
	upstreamBAddr := ""
	if cfg.UpstreamBBrokerHost != "" {
		upstreamBAddr = fmt.Sprintf("%s:%d", cfg.UpstreamBBrokerHost, cfg.UpstreamBBrokerPort)
	}
	health.SetUpstreams([]UpstreamInfo{
		{Label: "upstream_a", Host: upstreamAAddr, Client: upstreamAClient},
		{Label: "upstream_b", Host: upstreamBAddr, Client: upstreamBClient},
	})

	// Start periodic stats logging goroutine
	go func() {
		ticker := time.NewTicker(time.Duration(cfg.StatsInterval) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			slog.Info("Stats",
				"received", relay.stats.Received.Load(),
				"from_local", relay.stats.FromLocal.Load(),
				"from_up_a", relay.stats.FromUpA.Load(),
				"from_up_b", relay.stats.FromUpB.Load(),
				"relayed_in", relay.stats.RelayedIn.Load(),
				"relayed_out", relay.stats.RelayedOut.Load(),
				"dropped", relay.stats.Dropped.Load(),
				"cache_size", dedup.Size(),
			)
		}
	}()

	// Graceful shutdown on SIGTERM/SIGINT — uses SHUTDOWN_TIMEOUT_MS to allow
	// in-flight publishes to drain instead of being severed at 1s.
	// Declared early so the failover goroutine can use it as a stop signal.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Build the ordered candidate list for upstream-A failover:
	// [primary, fallback1, fallback2, …]. Only start the goroutine when there
	// is at least one fallback configured.
	if cfg.UpstreamABrokerHost != "" && len(cfg.UpstreamAFallbackHosts) > 0 {
		allUpstreamAHosts := append([]string{cfg.UpstreamABrokerHost}, cfg.UpstreamAFallbackHosts...)
		makeUpstreamA := func(name, brokerURL string) mqtt.Client {
			return makeClient(name, brokerURL,
				cfg.UpstreamABrokerUser, cfg.UpstreamABrokerPass,
				cfg.UpstreamABrokerTLS, cfg.UpstreamATLSServerName,
				false, relay.HandleUpstreamAMessage,
			)
		}
		go runUpstreamAFailover(ctx, cfg, allUpstreamAHosts, relay, health, makeUpstreamA)
		slog.Info("Upstream A failover enabled",
			"hosts", allUpstreamAHosts,
			"timeout_sec", cfg.UpstreamAFailoverTimeoutSec,
		)
	}

	<-ctx.Done()
	shutdownTimeout := uint(cfg.ShutdownTimeoutMS)
	slog.Info("Shutting down…", "timeout_ms", shutdownTimeout)
	localClient.Disconnect(shutdownTimeout)
	// Disconnect the current upstream-A client (may differ from upstreamAClient
	// if failover has rotated to a different host since startup).
	if current := relay.getUpA(); current != nil {
		current.Disconnect(shutdownTimeout)
	}
	if upstreamBClient != nil {
		upstreamBClient.Disconnect(shutdownTimeout)
	}
	slog.Info("Disconnected. Goodbye!")
}
