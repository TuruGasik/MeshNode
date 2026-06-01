// Package config assembles the per-module configurations into a single Config
// value. Each module owns its own Config struct, defaults, and env-var
// parsing — see internal/bmkg/config.go, internal/meshtastic/config.go,
// internal/bot/config.go, internal/hantavirus/config.go.
package config

import (
	"meshnode/autonotif/internal/bmkg"
	"meshnode/autonotif/internal/bot"
	"meshnode/autonotif/internal/hantavirus"
	"meshnode/autonotif/internal/meshtastic"
	"meshnode/autonotif/internal/util"
)

// Config is the top-level configuration consumed by main.
type Config struct {
	DryRun     bool
	Message    string
	BMKG       bmkg.Config
	Meshtastic meshtastic.Config
	Bot        bot.Config
	Hantavirus hantavirus.Config
}

// Load reads .env files and environment variables to build a Config.
func Load() Config {
	util.LoadDotEnv(".env", "../.env")
	return Config{
		DryRun:     util.GetEnvBool("DRY_RUN", false),
		Message:    util.GetEnv("MESSAGE", ""),
		BMKG:       bmkg.LoadConfig(),
		Meshtastic: meshtastic.LoadConfig(),
		Bot:        bot.LoadConfig(),
		Hantavirus: hantavirus.LoadConfig(),
	}
}
