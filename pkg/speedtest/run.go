package speedtest

import (
	"context"
	"fmt"
	"time"
)

// maxAsymmetry is the largest ratio between download and upload throughput
// that Result.Valid still considers plausible.
const maxAsymmetry = 100

// Result is the outcome of a full speedtest.
type Result struct {
	Server   Server
	Latency  Latency
	Download Throughput
	Upload   Throughput
	// Duration covers the latency, download, and upload tests, excluding
	// server selection.
	Duration time.Duration
}

// Valid reports whether the result is plausible: both directions moved
// data and neither is more than 100 times faster than the other.
func (r *Result) Valid() bool {
	down, up := r.Download.BitsPerSecond, r.Upload.BitsPerSecond

	return down > 0 && up > 0 && down <= up*maxAsymmetry && up <= down*maxAsymmetry
}

// Run selects the lowest-latency nearby server and measures latency,
// download, and upload against it.
func (c *Client) Run(ctx context.Context) (*Result, error) {
	server, err := c.SelectServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to select server: %w", err)
	}

	return c.RunServer(ctx, server)
}

// RunServer measures latency, download, and upload against target.
func (c *Client) RunServer(ctx context.Context, target Server) (*Result, error) {
	start := time.Now()

	latency, err := c.MeasureLatency(ctx, target)
	if err != nil {
		return nil, err
	}

	download, err := c.MeasureDownload(ctx, target)
	if err != nil {
		return nil, err
	}

	upload, err := c.MeasureUpload(ctx, target)
	if err != nil {
		return nil, err
	}

	return &Result{
		Server:   target,
		Latency:  latency,
		Download: download,
		Upload:   upload,
		Duration: time.Since(start),
	}, nil
}
