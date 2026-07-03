package main

import (
	"log/slog"
	"net/http"
)

// handleMetrics serves the cached speedtest metrics immediately and, if the
// cache is older than scrapeInterval, kicks off a background speedtest to
// refresh it without blocking the response (stale-while-revalidate). Per-
// server metric labels and multi-server testing are not yet implemented.
func (a *app) handleMetrics(writer http.ResponseWriter, request *http.Request) {
	// Intentionally not request.Context(): revalidation is a background job
	// scoped to the app's lifetime (a.baseCtx), not this single request.
	a.triggerRevalidation() //nolint:contextcheck

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
