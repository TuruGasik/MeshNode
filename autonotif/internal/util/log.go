package util

import (
	"log/slog"
	"os"
	"strings"
)

// SetupLogger configures the default slog handler with text output to stdout.
// Log level is INFO unless LOG_LEVEL=debug.
func SetupLogger() {
	logLevel := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))
}
