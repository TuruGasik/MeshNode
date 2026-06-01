package hantavirus

import "meshnode/autonotif/internal/util"

const DefaultDBPath = "/data/autonotif.db"

// Config holds Hantavirus pipeline configuration.
type Config struct {
	Once       bool
	DBPath     string
	RailwayURL string
}

// LoadConfig reads Hantavirus settings from environment variables, applying
// defaults for any missing values.
func LoadConfig() Config {
	return Config{
		Once:       util.GetEnvBool("HANTAVIRUS_ONCE", false),
		DBPath:     util.GetEnv("HANTAVIRUS_DB_PATH", DefaultDBPath),
		RailwayURL: util.GetEnv("HANTAVIRUS_RAILWAY_URL", ""),
	}
}
