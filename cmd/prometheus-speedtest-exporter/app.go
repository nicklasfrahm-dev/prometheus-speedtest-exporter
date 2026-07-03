package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
)

const mbpsToBps = 1e6

// app holds the dependencies shared by the HTTP handlers, plus the cache
// state guarded by mu. The cache is kept fresh by a background revalidation
// loop (run) rather than by incoming requests: handlers only ever read it,
// so /metrics never blocks on a speedtest and /readyz does not depend on
// ever being scraped.
type app struct {
	logger            *slog.Logger
	metrics           *Metrics
	prometheusHandler http.Handler
	scrapeInterval    time.Duration
	scrapeTimeout     time.Duration
	// runSpeedtest executes a full speedtest, observing ctx for cancellation.
	// Overridable in tests so revalidation logic can be exercised without
	// hitting the real network.
	runSpeedtest func(ctx context.Context) (*speedtest.Server, time.Duration, error)

	mu      sync.RWMutex
	lastRun time.Time
}

// run refreshes the cached metrics once immediately, then again every
// scrapeInterval, until ctx is canceled. It is meant to be started in its
// own goroutine for the lifetime of the process, so the cache is always
// populated independently of whether anything has scraped /metrics yet.
func (a *app) run(ctx context.Context) {
	a.revalidate(ctx)

	ticker := time.NewTicker(a.scrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.revalidate(ctx)
		}
	}
}

// revalidate runs a full speedtest, bounded by scrapeTimeout, and refreshes
// the cached metrics.
func (a *app) revalidate(ctx context.Context) {
	runCtx, cancel := context.WithTimeout(ctx, a.scrapeTimeout)
	defer cancel()

	target, elapsed, err := a.runSpeedtest(runCtx)

	a.mu.Lock()
	a.lastRun = time.Now()
	a.mu.Unlock()

	if err != nil {
		a.recordFailure(err)

		return
	}

	a.recordResults(target, elapsed)
}

// recordResults writes a completed speedtest's results into the gauges.
func (a *app) recordResults(target *speedtest.Server, elapsed time.Duration) {
	a.metrics.ping.Set(target.Latency.Seconds())
	a.metrics.jitter.Set(target.Jitter.Seconds())
	a.metrics.downloadSpeed.Set(target.DLSpeed.Mbps() * mbpsToBps)
	a.metrics.uploadSpeed.Set(target.ULSpeed.Mbps() * mbpsToBps)

	if target.CheckResultValid() {
		a.metrics.resultValid.Set(1)
	} else {
		a.metrics.resultValid.Set(0)
	}

	a.metrics.testDuration.Set(elapsed.Seconds())
	a.metrics.up.Set(1)
}

// recordFailure marks the last speedtest attempt as unsuccessful. Gauges
// other than up are left untouched, so scrapes keep serving the last
// known-good result while a persistently failing target is retried at most
// once per scrapeInterval instead of on every request.
func (a *app) recordFailure(err error) {
	a.metrics.up.Set(0)
	a.logger.Error("Failed to run speedtest", "error", err)
}

// handleMetrics serves the cached speedtest metrics. The cache is kept
// fresh by the background run loop, so this handler never blocks on a
// speedtest.
func (a *app) handleMetrics(writer http.ResponseWriter, request *http.Request) {
	a.prometheusHandler.ServeHTTP(writer, request)
}

// writeStatus writes an HTTP status code and its text body, logging any
// write failure.
func writeStatus(logger *slog.Logger, writer http.ResponseWriter, code int) {
	writer.WriteHeader(code)

	_, err := writer.Write([]byte(http.StatusText(code)))
	if err != nil {
		logger.Error("Failed to write response", "error", err)
	}
}

// handleLivez reports whether the process is up and serving requests.
func (a *app) handleLivez(writer http.ResponseWriter, _ *http.Request) {
	writeStatus(a.logger, writer, http.StatusOK)
}

// handleReadyz reports whether the cache holds at least one completed
// speedtest attempt, regardless of that attempt's outcome.
func (a *app) handleReadyz(writer http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	ready := !a.lastRun.IsZero()
	a.mu.RUnlock()

	if !ready {
		writeStatus(a.logger, writer, http.StatusServiceUnavailable)

		return
	}

	writeStatus(a.logger, writer, http.StatusOK)
}
