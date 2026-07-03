package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/showwin/speedtest-go/speedtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errStubSpeedtestFailure = errors.New("stub speedtest failure")

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

func TestAppHandleLivez(t *testing.T) {
	t.Parallel()

	application := &app{logger: slog.New(slog.DiscardHandler)}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/livez", nil)

	application.handleLivez(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, http.StatusText(http.StatusOK), recorder.Body.String())
}

func TestAppHandleReadyz(t *testing.T) {
	t.Parallel()

	application := &app{logger: slog.New(slog.DiscardHandler)}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)

	application.handleReadyz(recorder, request)

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, http.StatusText(http.StatusServiceUnavailable), recorder.Body.String())

	application.mu.Lock()
	application.lastRun = time.Now()
	application.mu.Unlock()

	recorder = httptest.NewRecorder()
	application.handleReadyz(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, http.StatusText(http.StatusOK), recorder.Body.String())
}

// TestParseDurationEnv cannot run in parallel: t.Setenv panics when called
// from a parallel test.
func TestParseDurationEnv(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	const fallback = 42 * time.Second

	cases := map[string]struct {
		raw  string
		want time.Duration
	}{
		"unset":   {raw: "", want: fallback},
		"valid":   {raw: "90s", want: 90 * time.Second},
		"invalid": {raw: "not-a-duration", want: fallback},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			const envVar = "TEST_PARSE_DURATION_ENV"

			t.Setenv(envVar, testCase.raw)

			assert.Equal(t, testCase.want, parseDurationEnv(logger, envVar, fallback))
		})
	}
}

func TestAppRevalidate(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	var calls atomic.Int32

	application := &app{
		logger:            slog.New(slog.DiscardHandler),
		metrics:           NewMetrics(reg),
		prometheusHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		scrapeTimeout:     time.Second,
		runSpeedtest: func(_ context.Context) (*speedtest.Server, time.Duration, error) {
			calls.Add(1)

			return nil, 0, errStubSpeedtestFailure
		},
	}

	application.revalidate(t.Context())

	application.mu.RLock()
	defer application.mu.RUnlock()

	assert.False(t, application.lastRun.IsZero(), "revalidate must refresh lastRun")
	assert.Equal(t, int32(1), calls.Load())
}

// TestAppRunRevalidatesPeriodically exercises the background loop that
// replaced request-triggered revalidation: previously the cache only ever
// refreshed inside handleMetrics, so /readyz could never turn healthy until
// something scraped /metrics — a startup deadlock if traffic to /metrics is
// itself gated on readiness. run() must populate the cache on its own,
// independent of any request, and keep refreshing on a ticker until its
// context is canceled.
func TestAppRunRevalidatesPeriodically(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	var calls atomic.Int32

	application := &app{
		logger:            slog.New(slog.DiscardHandler),
		metrics:           NewMetrics(reg),
		prometheusHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		scrapeInterval:    10 * time.Millisecond,
		scrapeTimeout:     time.Second,
		runSpeedtest: func(_ context.Context) (*speedtest.Server, time.Duration, error) {
			calls.Add(1)

			return nil, 0, errStubSpeedtestFailure
		},
	}

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		application.run(ctx)
		close(done)
	}()

	require.Eventually(t, func() bool {
		return calls.Load() >= 3
	}, 5*time.Second, 10*time.Millisecond, "run must revalidate immediately and then on a ticker")

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop after context cancellation")
	}
}

func TestAppRecordResults(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	application := &app{
		logger:            slog.New(slog.DiscardHandler),
		metrics:           NewMetrics(reg),
		prometheusHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	}

	// speedtest.ByteRate is bytes/s; Mbps() divides by 125000, so multiply
	// back to construct a fixture in Mbps.
	const bytesPerSecondPerMbps = 125000

	target := &speedtest.Server{
		Latency: 50 * time.Millisecond,
		Jitter:  5 * time.Millisecond,
		DLSpeed: 100 * bytesPerSecondPerMbps,
		ULSpeed: 20 * bytesPerSecondPerMbps,
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
