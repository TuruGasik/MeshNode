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
	LocalBrokerHost     string
	LocalBrokerPort     int
	LocalBrokerUsername string
	LocalBrokerPassword string
	UpstreamABrokerHost string
	UpstreamABrokerPort int
	UpstreamABrokerUser string
	UpstreamABrokerPass string
	UpstreamBBrokerHost string
	UpstreamBBrokerPort int
	UpstreamBBrokerUser string
	UpstreamBBrokerPass string
	TopicRoot           string
	DedupTTL            int
	CleanupInterval     int
	LogLevel            string
}

// LoadConfig reads configuration from environment variables with defaults.
func LoadConfig() *Config {
	return &Config{
		LocalBrokerHost:     getEnv("LOCAL_MQTT_HOST", "meshnode-mqtt"),
		LocalBrokerPort:     getEnvInt("LOCAL_MQTT_PORT", 1883),
		LocalBrokerUsername: getEnv("LOCAL_MQTT_USERNAME", "mqtt-relay"),
		LocalBrokerPassword: getEnv("LOCAL_MQTT_PASSWORD", ""),
		UpstreamABrokerHost: getEnv("UPSTREAM_A_HOST", "mqtt.meshnode.id"),
		UpstreamABrokerPort: getEnvInt("UPSTREAM_A_PORT", 1883),
		UpstreamABrokerUser: getEnv("UPSTREAM_A_USERNAME", "idmeshnode"),
		UpstreamABrokerPass: getEnv("UPSTREAM_A_PASSWORD", "Mesh4all"),
		UpstreamBBrokerHost: getEnv("UPSTREAM_B_HOST", "mqtt.meshtastic.org"),
		UpstreamBBrokerPort: getEnvInt("UPSTREAM_B_PORT", 1883),
		UpstreamBBrokerUser: getEnv("UPSTREAM_B_USERNAME", "meshdev"),
		UpstreamBBrokerPass: getEnv("UPSTREAM_B_PASSWORD", "large4cats"),
		TopicRoot:           getEnv("TOPIC_ROOT", "msh/ID/#"),
		DedupTTL:            getEnvInt("DEDUP_TTL", 600),
		CleanupInterval:     getEnvInt("CLEANUP_INTERVAL", 60),
		LogLevel:            getEnv("LOG_LEVEL", "INFO"),
	}
}

func (c *Config) TopicMatcher(topic string) bool {
	if strings.HasSuffix(c.TopicRoot, "/#") {
		prefix := strings.TrimSuffix(c.TopicRoot, "#")
		return strings.HasPrefix(topic, prefix)
	}
	return topic == c.TopicRoot
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
		"local", fmt.Sprintf("%s:%d", cfg.LocalBrokerHost, cfg.LocalBrokerPort),
		"upstream_a", fmt.Sprintf("%s:%d", cfg.UpstreamABrokerHost, cfg.UpstreamABrokerPort),
		"upstream_b", fmt.Sprintf("%s:%d", cfg.UpstreamBBrokerHost, cfg.UpstreamBBrokerPort),
		"topic_root", cfg.TopicRoot,
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

	makeClient := func(name, broker, username, password string, handler mqtt.MessageHandler) mqtt.Client {
		opts := mqtt.NewClientOptions().
			AddBroker(broker).
			SetClientID(name).
			SetUsername(username).
			SetPassword(password).
			SetCleanSession(true).
			SetAutoReconnect(true).
			SetOrderMatters(false).
			SetConnectRetry(true).
			SetConnectRetryInterval(5 * time.Second).
			SetMaxReconnectInterval(30 * time.Second).
			SetKeepAlive(60 * time.Second).
			SetDefaultPublishHandler(handler).
			SetOnConnectHandler(func(client mqtt.Client) {
				slog.Info("Connected to MQTT broker", "name", name, "broker", broker)
				if token := client.Subscribe(cfg.TopicRoot, 0, nil); token.Wait() && token.Error() != nil {
					slog.Error("Failed to subscribe",
						"name", name,
						"topic", cfg.TopicRoot,
						"error", token.Error(),
					)
				} else {
					slog.Info("Subscribed", "name", name, "topic", cfg.TopicRoot)
				}
				health.SetConnected(true)
			}).
			SetConnectionLostHandler(func(client mqtt.Client, err error) {
				slog.Warn("Connection lost, will auto-reconnect…", "name", name, "error", err)
				if name == "mqtt-relay-local" {
					health.SetConnected(false)
				}
			}).
			SetReconnectingHandler(func(client mqtt.Client, opts *mqtt.ClientOptions) {
				slog.Info("Reconnecting to MQTT broker…", "name", name)
			})

		client := mqtt.NewClient(opts)
		if token := client.Connect(); token.Wait() && token.Error() != nil {
			slog.Error("Failed to connect to MQTT broker", "name", name, "error", token.Error())
			os.Exit(1)
		}
		return client
	}

	localBroker := fmt.Sprintf("tcp://%s:%d", cfg.LocalBrokerHost, cfg.LocalBrokerPort)
	upstreamABroker := fmt.Sprintf("tcp://%s:%d", cfg.UpstreamABrokerHost, cfg.UpstreamABrokerPort)
	upstreamBBroker := fmt.Sprintf("tcp://%s:%d", cfg.UpstreamBBrokerHost, cfg.UpstreamBBrokerPort)

	localClient := makeClient("mqtt-relay-local", localBroker, cfg.LocalBrokerUsername, cfg.LocalBrokerPassword, relay.HandleLocalMessage)
	upstreamAClient := makeClient("mqtt-relay-upstream-a", upstreamABroker, cfg.UpstreamABrokerUser, cfg.UpstreamABrokerPass, relay.HandleUpstreamAMessage)
	upstreamBClient := makeClient("mqtt-relay-upstream-b", upstreamBBroker, cfg.UpstreamBBrokerUser, cfg.UpstreamBBrokerPass, relay.HandleUpstreamBMessage)
	relay.SetClients(localClient, upstreamAClient, upstreamBClient)

	// Start periodic stats logging goroutine
	go func() {
		ticker := time.NewTicker(time.Duration(cfg.CleanupInterval) * time.Second)
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

	// Graceful shutdown on SIGTERM/SIGINT
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	<-ctx.Done()
	slog.Info("Shutting down…")
	localClient.Disconnect(1000)
	upstreamAClient.Disconnect(1000)
	upstreamBClient.Disconnect(1000)
	slog.Info("Disconnected. Goodbye!")
}
