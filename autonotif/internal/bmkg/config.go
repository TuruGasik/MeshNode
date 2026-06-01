package bmkg

import (
	"strings"
	"time"

	"meshnode/autonotif/internal/util"
)

const (
	SourceBMKG     = "bmkg"
	SourceInatews2 = "inatews2"

	DefaultURL               = "https://data.bmkg.go.id/DataMKG/TEWS/autogempa.json"
	DefaultInatews2URL       = "https://bmkg-content-inatews.storage.googleapis.com/live30event.xml"
	DefaultStateFile         = "/data/autonotif-bmkg-state.json"
	DefaultInatews2StateFile = "/data/autonotif-bmkg-inatews2-state.json"
	DefaultPollInterval      = 2 * time.Second
)

// Config holds BMKG poller configuration.
type Config struct {
	Source        string
	URL           string
	Inatews2URL   string
	PollInterval  time.Duration
	Once          bool
	SendOnStart   bool
	StateFile     string
	MinMagnitude  float64
}

// LoadConfig reads BMKG settings from environment variables, applying
// defaults for any missing values.
func LoadConfig() Config {
	source := strings.ToLower(util.GetEnv("BMKG_SOURCE", SourceBMKG))
	return Config{
		Source:       source,
		URL:          util.GetEnv("BMKG_URL", DefaultURL),
		Inatews2URL:  util.GetEnvAny([]string{"BMKG_INATEWS2_URL", "INATEWS2_URL"}, DefaultInatews2URL),
		PollInterval: util.GetEnvDuration("POLL_INTERVAL", DefaultPollInterval),
		Once:         util.GetEnvBool("ONCE", false),
		SendOnStart:  util.GetEnvBool("SEND_ON_START", false),
		StateFile:    util.GetEnv("STATE_FILE", defaultStateFileForSource(source)),
		MinMagnitude: util.GetEnvFloat("MIN_MAGNITUDE", 0),
	}
}

func defaultStateFileForSource(source string) string {
	if source == SourceInatews2 {
		return DefaultInatews2StateFile
	}
	return DefaultStateFile
}
