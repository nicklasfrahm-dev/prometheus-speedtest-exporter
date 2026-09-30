package main

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nicklasfrahm-dev/prometheus-speedtest-exporter/pkg/speedtest"
)

const (
	defaultScrapeInterval = time.Hour
	defaultScrapeTimeout  = 5 * time.Minute
)

// sizeUnits maps the suffixes accepted by parseSizeEnv to their multiplier,
// longest suffix first so that "KiB" is not mistaken for "B".
//
//nolint:gochecknoglobals // Read-only lookup table.
var sizeUnits = []struct {
	suffix     string
	multiplier int64
}{
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"B", 1},
}

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

// parseIntEnv reads an integer from the named environment variable,
// falling back to the given default if it is unset or invalid.
func parseIntEnv(logger *slog.Logger, name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil {
		logger.Warn("Invalid integer, using default", "variable", name, "value", raw, "default", fallback)

		return fallback
	}

	return parsed
}

// parseSizeEnv reads a size in bytes from the named environment variable,
// accepting an optional B, KiB, MiB, or GiB suffix, and falls back to the
// given default if it is unset or invalid.
func parseSizeEnv(logger *slog.Logger, name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}

	number, multiplier := raw, int64(1)

	for _, unit := range sizeUnits {
		if trimmed, ok := strings.CutSuffix(raw, unit.suffix); ok {
			number, multiplier = trimmed, unit.multiplier

			break
		}
	}

	parsed, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
	if err != nil || parsed > (1<<63-1)/multiplier {
		logger.Warn("Invalid size, using default", "variable", name, "value", raw, "default", fallback)

		return fallback
	}

	return parsed * multiplier
}

// speedtestOptions builds the speedtest client options from SPEEDTEST_*
// environment variables. Values the library rejects, such as negative
// counts, surface as an error from speedtest.New.
func speedtestOptions(logger *slog.Logger) []speedtest.Option {
	return []speedtest.Option{
		speedtest.WithUserAgent(speedtest.DefaultUserAgent + "/" + version),
		speedtest.WithServerCandidates(
			parseIntEnv(logger, "SPEEDTEST_SERVER_CANDIDATES", speedtest.DefaultServerCandidates),
		),
		speedtest.WithPingCount(parseIntEnv(logger, "SPEEDTEST_PING_COUNT", speedtest.DefaultPingCount)),
		speedtest.WithDuration(parseDurationEnv(logger, "SPEEDTEST_DURATION", speedtest.DefaultDuration)),
		speedtest.WithWarmup(parseDurationEnv(logger, "SPEEDTEST_WARMUP", speedtest.DefaultWarmup)),
		speedtest.WithStreams(parseIntEnv(logger, "SPEEDTEST_STREAMS", 0)),
		speedtest.WithMaxStreams(parseIntEnv(logger, "SPEEDTEST_MAX_STREAMS", speedtest.DefaultMaxStreams)),
		speedtest.WithDownloadSize(parseIntEnv(logger, "SPEEDTEST_DOWNLOAD_SIZE", speedtest.DefaultDownloadSize)),
		speedtest.WithUploadSize(parseSizeEnv(logger, "SPEEDTEST_UPLOAD_SIZE", speedtest.DefaultUploadSize)),
		speedtest.WithBufferSize(
			int(parseSizeEnv(logger, "SPEEDTEST_BUFFER_SIZE", speedtest.DefaultBufferSize)),
		),
	}
}
