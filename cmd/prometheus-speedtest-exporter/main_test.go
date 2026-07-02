package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/showwin/speedtest-go/speedtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"Info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"WARNING": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelWarn,
		"bogus":   slog.LevelWarn,
	}

	for input, want := range cases {
		assert.Equalf(t, want, parseLogLevel(input), "parseLogLevel(%q)", input)
	}
}

// TestNewLogger cannot run in parallel: t.Setenv panics when called from a
// parallel test.
//
//nolint:paralleltest
func TestNewLogger(t *testing.T) {
	cases := []string{"json", "console", "text", "", "bogus"}

	for _, format := range cases {
		t.Setenv("LOG_FORMAT", format)
		t.Setenv("LOG_LEVEL", "info")

		assert.NotNilf(t, newLogger(), "newLogger() with LOG_FORMAT=%q", format)
	}
}

func TestNewMetrics(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	NewMetrics(reg)

	families, err := reg.Gather()
	require.NoError(t, err)

	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.GetName())
	}

	assert.ElementsMatch(t, []string{
		"speedtest_download_speed_bps",
		"speedtest_upload_speed_bps",
		"speedtest_ping_seconds",
		"speedtest_jitter_seconds",
		"speedtest_result_valid",
		"speedtest_test_duration_seconds",
		"speedtest_up",
	}, names)
}

func TestAppHandleHealthz(t *testing.T) {
	t.Parallel()

	application := &app{logger: slog.New(slog.DiscardHandler)}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)

	application.handleHealthz(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, http.StatusText(http.StatusOK), recorder.Body.String())
}

func TestAppRecordResults(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	application := &app{
		logger:            slog.New(slog.DiscardHandler),
		metrics:           NewMetrics(reg),
		prometheusHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	}

	target := &speedtest.Server{
		Latency: 50 * time.Millisecond,
		Jitter:  5 * time.Millisecond,
		DLSpeed: 100,
		ULSpeed: 20,
	}

	application.recordResults(target, 2*time.Second)

	const delta = 1e-9

	assert.InDelta(t, 0.05, testutil.ToFloat64(application.metrics.ping), delta)
	assert.InDelta(t, 0.005, testutil.ToFloat64(application.metrics.jitter), delta)
	assert.InDelta(t, 100*mbpsToBps, testutil.ToFloat64(application.metrics.downloadSpeed), delta)
	assert.InDelta(t, 20*mbpsToBps, testutil.ToFloat64(application.metrics.uploadSpeed), delta)
	assert.InDelta(t, 1, testutil.ToFloat64(application.metrics.resultValid), delta)
	assert.InDelta(t, 2, testutil.ToFloat64(application.metrics.testDuration), delta)
	assert.InDelta(t, 1, testutil.ToFloat64(application.metrics.up), delta)
}
