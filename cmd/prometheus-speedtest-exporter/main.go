// Command prometheus-speedtest-exporter serves speedtest.net results as
// Prometheus metrics on every scrape of /metrics.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultPort       = "9516"
	shutdownTimeout   = 5 * time.Second
	readHeaderTimeout = 5 * time.Second
)

var version = "dev"

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

	scrapeInterval := parseDurationEnv(logger, "SCRAPE_INTERVAL", defaultScrapeInterval)
	scrapeTimeout := parseDurationEnv(logger, "SCRAPE_TIMEOUT", defaultScrapeTimeout)

	baseCtx, cancelBaseCtx := context.WithCancel(context.Background())

	reg := prometheus.NewRegistry()

	application := &app{
		logger:            logger,
		metrics:           NewMetrics(reg),
		prometheusHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		scrapeInterval:    scrapeInterval,
		scrapeTimeout:     scrapeTimeout,
		baseCtx:           baseCtx,
		runSpeedtest:      runFullSpeedtest,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", application.handleMetrics)
	mux.HandleFunc("/livez", application.handleLivez)
	mux.HandleFunc("/readyz", application.handleReadyz)

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

	cancelBaseCtx()

	if err != nil {
		logger.Error("Failed to shut down server gracefully", "error", err)
		os.Exit(1)
	}
}
