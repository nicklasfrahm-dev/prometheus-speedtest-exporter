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

// app holds the dependencies shared by the HTTP handlers, plus the
// stale-while-revalidate cache state guarded by mu.
type app struct {
	logger            *slog.Logger
	metrics           *Metrics
	prometheusHandler http.Handler
	scrapeInterval    time.Duration
	scrapeTimeout     time.Duration
	// baseCtx is deliberately not the request context: it is the
	// process/server lifetime, used to cancel background revalidations on
	// shutdown without tying them to any single scrape's request lifecycle.
	baseCtx context.Context //nolint:containedctx
	// runSpeedtest executes a full speedtest, observing ctx for cancellation.
	// Overridable in tests so revalidation logic can be exercised without
	// hitting the real network.
	runSpeedtest func(ctx context.Context) (*speedtest.Server, time.Duration, error)

	mu           sync.RWMutex
	lastRun      time.Time
	revalidating bool
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

// revalidate runs a full speedtest, bounded by scrapeTimeout, and refreshes
// the cached metrics. It always marks the cache fresh afterwards so a
// scrapeInterval elapses before the next attempt, whether this one
// succeeded or failed.
func (a *app) revalidate() {
	defer func() {
		a.mu.Lock()
		a.lastRun = time.Now()
		a.revalidating = false
		a.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(a.baseCtx, a.scrapeTimeout)
	defer cancel()

	target, elapsed, err := a.runSpeedtest(ctx)
	if err != nil {
		a.recordFailure(err)

		return
	}

	a.recordResults(target, elapsed)
}

// triggerRevalidation starts a background revalidate() if the cache is
// stale and no revalidation is already in flight, using double-checked
// locking so concurrent scrapes only ever spawn one.
func (a *app) triggerRevalidation() {
	a.mu.RLock()
	stale := time.Since(a.lastRun) >= a.scrapeInterval
	inFlight := a.revalidating
	a.mu.RUnlock()

	if !stale || inFlight {
		return
	}

	a.mu.Lock()

	if a.revalidating {
		a.mu.Unlock()

		return
	}

	a.revalidating = true

	a.mu.Unlock()

	go a.revalidate()
}
