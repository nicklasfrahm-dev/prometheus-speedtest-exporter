package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/lmittmann/tint"
)

// newLogger builds a slog.Logger configured via the LOG_LEVEL and LOG_FORMAT
// environment variables.
func newLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLogLevel(os.Getenv("LOG_LEVEL"))}

	var handler slog.Handler

	switch strings.ToLower(os.Getenv("LOG_FORMAT")) {
	case "console", "text":
		handler = tint.NewHandler(os.Stdout, &tint.Options{Level: opts.Level})
	default: // "json" and anything unrecognized default to JSON.
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// parseLogLevel maps LOG_LEVEL to a slog.Level, defaulting to warn.
func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	default:
		return slog.LevelWarn
	}
}
