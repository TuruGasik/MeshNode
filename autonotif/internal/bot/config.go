package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"meshnode/autonotif/internal/util"
)

const (
	DefaultConfigFile = "/data/autonotif-bot-config.json"
	DefaultAdminNode  = 0xaf1e4204
)

// Config holds responder configuration loaded from environment variables.
type Config struct {
	EnableResponder bool
	ReplyToDM       bool
	AdminNodes      []uint32
	ConfigFile      string
}

// RuntimeConfig holds operator-tunable settings persisted to disk.
type RuntimeConfig struct {
	ReplyToDM           bool     `json:"reply_to_dm"`
	UnknownCommandReply bool     `json:"unknown_command_reply"`
	CommandPrefixes     []string `json:"command_prefixes"`
}

// DefaultRuntimeConfig returns the runtime defaults applied when no config
// file exists on disk.
func DefaultRuntimeConfig(replyToDM bool) RuntimeConfig {
	return RuntimeConfig{
		ReplyToDM:           replyToDM,
		UnknownCommandReply: false,
		CommandPrefixes:     []string{"/", "."},
	}
}

// LoadConfig reads bot settings from environment variables, applying defaults
// for any missing values.
func LoadConfig() Config {
	return Config{
		EnableResponder: util.GetEnvBool("BOT_ENABLE_RESPONDER", true),
		ReplyToDM:       util.GetEnvBool("BOT_REPLY_TO_DM", true),
		AdminNodes:      util.GetEnvNodeList("BOT_ADMIN_NODES", []uint32{DefaultAdminNode}),
		ConfigFile:      util.GetEnv("BOT_CONFIG_FILE", DefaultConfigFile),
	}
}

// LoadRuntimeConfig reads the on-disk RuntimeConfig at path, falling back to
// the supplied defaults when the file does not exist (the file is created on
// first run). Reads of an existing valid file do not rewrite it.
func LoadRuntimeConfig(path string, fallback RuntimeConfig) (RuntimeConfig, error) {
	if strings.TrimSpace(path) == "" {
		return fallback, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := SaveRuntimeConfig(path, fallback); err != nil {
				return fallback, err
			}
			return fallback, nil
		}
		return fallback, err
	}
	var runtime RuntimeConfig
	if err := json.Unmarshal(data, &runtime); err != nil {
		return fallback, err
	}
	if len(runtime.CommandPrefixes) == 0 {
		runtime.CommandPrefixes = append([]string(nil), fallback.CommandPrefixes...)
	}
	return runtime, nil
}

// SaveRuntimeConfig persists runtime to path with secure permissions.
func SaveRuntimeConfig(path string, runtime RuntimeConfig) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(runtime, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
