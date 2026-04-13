package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Config holds all configuration loaded from environment variables.
// 100% compatible with the Python relay.py env vars.
type Config struct {
	MQTTHost          string
	MQTTPort          int
	MQTTUsername      string
	MQTTPassword      string
	SubscribeTopic    string // e.g. "msh/ID/#" — local client + relay self-echo
	SubscribeBridgeIn string // e.g. "msh/bridge_in/ID/#" — raw bridge inbound
	RelayPrefix       string // e.g. "msh/relay/" — outbound to bridges
	SourcePrefix      string // e.g. "msh/" — canonical topic prefix
	BridgeInPrefix    string // e.g. "msh/bridge_in/" — bridge inbound prefix
	DedupTTL          int    // seconds
	CleanupInterval   int    // seconds
	LogLevel          string
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig() *Config {
	return &Config{
		MQTTHost:          getEnv("MQTT_HOST", "meshnode-mqtt"),
		MQTTPort:          getEnvInt("MQTT_PORT", 1883),
		MQTTUsername:      getEnv("MQTT_USERNAME", "mqtt-relay"),
		MQTTPassword:      getEnv("MQTT_PASSWORD", ""),
		SubscribeTopic:    getEnv("SUBSCRIBE_TOPIC", "msh/ID/#"),
		SubscribeBridgeIn: getEnv("SUBSCRIBE_BRIDGE_IN", "msh/bridge_in/ID/#"),
		RelayPrefix:       getEnv("RELAY_PREFIX", "msh/relay/"),
		SourcePrefix:      getEnv("SOURCE_PREFIX", "msh/"),
		BridgeInPrefix:    getEnv("BRIDGE_IN_PREFIX", "msh/bridge_in/"),
		DedupTTL:          getEnvInt("DEDUP_TTL", 600),
		CleanupInterval:   getEnvInt("CLEANUP_INTERVAL", 60),
		LogLevel:          getEnv("LOG_LEVEL", "INFO"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
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

// HealthState tracks the health of the relay service.
type HealthState struct {
	connected atomic.Bool
	lastMsgAt atomic.Int64 // unix timestamp
	startedAt time.Time
	stats     *Stats
	dedupSize func() int
}

// NewHealthState creates a new HealthState tracker.
func NewHealthState(stats *Stats, dedupSize func() int) *HealthState {
	return &HealthState{
		stats:     stats,
		dedupSize: dedupSize,
		startedAt: time.Now(),
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
		"stats": map[string]int64{
			"received":    h.stats.Received.Load(),
			"relayed_in":  h.stats.RelayedIn.Load(),
			"relayed_out": h.stats.RelayedOut.Load(),
			"dropped":     h.stats.Dropped.Load(),
			"cache_size":  int64(h.dedupSize()),
		},
	}
}

// startHealthServer starts an HTTP server exposing /health endpoint.
func startHealthServer(health *HealthState, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		data := health.Status()
		status := data["status"].(string)
		if status == "healthy" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(data)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
	})

	go func() {
		slog.Info("Health server listening", "addr", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("Health server failed", "error", err)
		}
	}()
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
		"broker", fmt.Sprintf("%s:%d", cfg.MQTTHost, cfg.MQTTPort),
		"sub_bridge_in", cfg.SubscribeBridgeIn,
		"sub_local", cfg.SubscribeTopic,
		"pub_clean", cfg.SourcePrefix+"<subtopic>",
		"pub_relay_out", cfg.RelayPrefix+"<subtopic>",
		"bridge_in_pfx", cfg.BridgeInPrefix,
		"dedup_ttl", fmt.Sprintf("%ds", cfg.DedupTTL),
		"cleanup_interval", fmt.Sprintf("%ds", cfg.CleanupInterval),
		"log_level", cfg.LogLevel,
	)
	slog.Info("============================================================")

	// Initialize dedup store
	dedup := NewDedupStore(time.Duration(cfg.DedupTTL) * time.Second)

	// Initialize health tracker
	health := NewHealthState(nil, dedup.Size)
	startHealthServer(health, ":8081")

	// Initialize relay with health callback
	relay := NewRelay(cfg, dedup, health.Touch)
	health.stats = relay.stats

	// Start cleanup goroutine
	go dedup.CleanupLoop(time.Duration(cfg.CleanupInterval) * time.Second)

	// MQTT client options
	broker := fmt.Sprintf("tcp://%s:%d", cfg.MQTTHost, cfg.MQTTPort)
	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID("mqtt-relay-dedup").
		SetUsername(cfg.MQTTUsername).
		SetPassword(cfg.MQTTPassword).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetOrderMatters(false). // parallel message processing — no queuing behind DROPs
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		SetKeepAlive(60 * time.Second).
		SetDefaultPublishHandler(relay.HandleMessage).
		SetOnConnectHandler(func(client mqtt.Client) {
			slog.Info("Connected to MQTT broker", "broker", broker)
			health.SetConnected(true)

			// Subscribe to bridge inbound (raw, potentially duplicated)
			if token := client.Subscribe(cfg.SubscribeBridgeIn, 0, nil); token.Wait() && token.Error() != nil {
				slog.Error("Failed to subscribe to bridge_in topic",
					"topic", cfg.SubscribeBridgeIn,
					"error", token.Error(),
				)
			} else {
				slog.Info("Subscribed (bridge inbound)", "topic", cfg.SubscribeBridgeIn)
			}

			// Subscribe to local client messages (for outbound relay)
			if token := client.Subscribe(cfg.SubscribeTopic, 0, nil); token.Wait() && token.Error() != nil {
				slog.Error("Failed to subscribe to local topic",
					"topic", cfg.SubscribeTopic,
					"error", token.Error(),
				)
			} else {
				slog.Info("Subscribed (local clients)", "topic", cfg.SubscribeTopic)
			}
		}).
		SetConnectionLostHandler(func(client mqtt.Client, err error) {
			slog.Warn("Connection lost, will auto-reconnect…", "error", err)
			health.SetConnected(false)
		}).
		SetReconnectingHandler(func(client mqtt.Client, opts *mqtt.ClientOptions) {
			slog.Info("Reconnecting to MQTT broker…")
		})

	// Connect
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		slog.Error("Failed to connect to MQTT broker", "error", token.Error())
		os.Exit(1)
	}

	// Start periodic stats logging goroutine
	go func() {
		ticker := time.NewTicker(time.Duration(cfg.CleanupInterval) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			slog.Info("Stats",
				"received", relay.stats.Received.Load(),
				"relayed_in", relay.stats.RelayedIn.Load(),
				"relayed_out", relay.stats.RelayedOut.Load(),
				"dropped", relay.stats.Dropped.Load(),
				"cache_size", dedup.Size(),
			)
		}
	}()

	// Graceful shutdown on SIGTERM/SIGINT
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	<-ctx.Done()
	slog.Info("Shutting down…")
	client.Disconnect(1000) // wait up to 1s for in-flight messages
	slog.Info("Disconnected. Goodbye!")
}
