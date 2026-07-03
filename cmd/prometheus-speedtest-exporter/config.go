package main

import (
	"log/slog"
	"os"
	"time"
)

const (
	defaultScrapeInterval = time.Hour
	defaultScrapeTimeout  = 5 * time.Minute
)

// parseDurationEnv reads a duration from the named environment variable,
// falling back to the given default if it is unset or invalid.
func parseDurationEnv(logger *slog.Logger, name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		logger.Warn("Invalid duration, using default", "variable", name, "value", raw, "default", fallback)

		return fallback
	}

	return parsed
}
