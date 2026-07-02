// Command prometheus-speedtest-exporter serves speedtest.net results as
// Prometheus metrics on every scrape of /metrics.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/showwin/speedtest-go/speedtest"
)

const (
	metricsNamespace  = "speedtest"
	defaultPort       = "9516"
	mbpsToBps         = 1e6
	shutdownTimeout   = 5 * time.Second
	readHeaderTimeout = 5 * time.Second
)

var version = "dev"

var errNoServerFound = errors.New("no available server found")

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

type Metrics struct {
	downloadSpeed prometheus.Gauge
	uploadSpeed   prometheus.Gauge
	ping          prometheus.Gauge
	jitter        prometheus.Gauge
	resultValid   prometheus.Gauge
	testDuration  prometheus.Gauge
	up            prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	gauges := &Metrics{
		downloadSpeed: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "download_speed_bps",
			Help:      "Download speed (bit/s)",
		}),
		uploadSpeed: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "upload_speed_bps",
				Help:      "Upload speed (bit/s)",
			},
		),
		ping: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "ping_seconds",
				Help:      "Latency (seconds)",
			},
		),
		jitter: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "jitter_seconds",
				Help:      "Jitter (seconds)",
			},
		),
		resultValid: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "result_valid",
				Help:      "Indicates if the result is logical given UL and DL speed",
			},
		),
		testDuration: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "test_duration_seconds",
				Help:      "Duration of the test (seconds)",
			},
		),
		up: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "up",
				Help:      "Indicates if the last speedtest was successful",
			},
		),
	}

	reg.MustRegister(gauges.downloadSpeed)
	reg.MustRegister(gauges.uploadSpeed)
	reg.MustRegister(gauges.ping)
	reg.MustRegister(gauges.jitter)
	reg.MustRegister(gauges.resultValid)
	reg.MustRegister(gauges.testDuration)
	reg.MustRegister(gauges.up)

	return gauges
}

// app holds the dependencies shared by the HTTP handlers.
type app struct {
	logger            *slog.Logger
	metrics           *Metrics
	prometheusHandler http.Handler
}

// findNearestServer resolves the speedtest.net server with the lowest
// network distance to the current host.
func findNearestServer(client *speedtest.Speedtest) (*speedtest.Server, error) {
	userInfo, err := client.FetchUserInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	serverList, err := client.FetchServers(userInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch server list: %w", err)
	}

	targets, err := serverList.Available().FindServer([]int{})
	if len(targets) == 0 {
		err = errNoServerFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to find available server: %w", err)
	}

	minDistance := math.MaxFloat64

	var target *speedtest.Server

	for _, candidate := range targets {
		if candidate.Distance < minDistance {
			minDistance = candidate.Distance
			target = candidate
		}
	}

	return target, nil
}

// recordResults writes a completed speedtest's results into the gauges.
func (a *app) recordResults(target *speedtest.Server, elapsed time.Duration) {
	a.metrics.ping.Set(target.Latency.Seconds())
	a.metrics.jitter.Set(target.Jitter.Seconds())
	a.metrics.downloadSpeed.Set(target.DLSpeed * mbpsToBps)
	a.metrics.uploadSpeed.Set(target.ULSpeed * mbpsToBps)

	if target.CheckResultValid() {
		a.metrics.resultValid.Set(1)
	} else {
		a.metrics.resultValid.Set(0)
	}

	a.metrics.testDuration.Set(elapsed.Seconds())
	a.metrics.up.Set(1)
}

// handleMetrics runs a speedtest and serves the resulting Prometheus
// metrics. Per-server metric labels and multi-server testing are not yet
// implemented.
func (a *app) handleMetrics(writer http.ResponseWriter, request *http.Request) {
	fail := func(err error) {
		a.metrics.up.Set(0)
		a.logger.Error("Failed to run speedtest", "error", err)
		a.prometheusHandler.ServeHTTP(writer, request)
	}

	client := speedtest.New()

	target, err := findNearestServer(client)
	if err != nil {
		fail(err)

		return
	}

	start := time.Now()

	err = target.PingTest()
	if err != nil {
		fail(fmt.Errorf("failed to run ping test: %w", err))

		return
	}

	err = target.DownloadTest()
	if err != nil {
		fail(fmt.Errorf("failed to run download test: %w", err))

		return
	}

	err = target.UploadTest()
	if err != nil {
		fail(fmt.Errorf("failed to run upload test: %w", err))

		return
	}

	elapsed := time.Since(start)

	a.recordResults(target, elapsed)

	a.prometheusHandler.ServeHTTP(writer, request)
}

func (a *app) handleHealthz(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusOK)

	_, err := writer.Write([]byte(http.StatusText(http.StatusOK)))
	if err != nil {
		a.logger.Error("Failed to write health check response", "error", err)
	}
}

// waitForShutdown blocks until SIGINT or SIGTERM is received, then gracefully
// shuts down server.
func waitForShutdown(logger *slog.Logger, server *http.Server) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received signal", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("failed to shut down server: %w", err)
	}

	return nil
}

func main() {
	logger := newLogger()

	port := ":" + os.Getenv("PORT")
	if port == ":" {
		port = ":" + defaultPort
	}

	reg := prometheus.NewRegistry()

	application := &app{
		logger:            logger,
		metrics:           NewMetrics(reg),
		prometheusHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", application.handleMetrics)
	mux.HandleFunc("/healthz", application.handleHealthz)

	server := &http.Server{
		Addr:              port,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	logger.Info("Starting application", "application", "prometheus-speedtest-exporter", "version", version)

	go func() {
		logger.Info("Starting server", "address", "http://0.0.0.0"+port)

		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	err := waitForShutdown(logger, server)
	if err != nil {
		logger.Error("Failed to shut down server gracefully", "error", err)
		os.Exit(1)
	}
}
