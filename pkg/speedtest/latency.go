package speedtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

// Latency summarizes round-trip times to a server.
type Latency struct {
	Mean time.Duration
	Min  time.Duration
	Max  time.Duration
	// Jitter is the mean absolute difference between consecutive samples,
	// matching how speedtest.net reports it.
	Jitter  time.Duration
	Samples []time.Duration
}

// MeasureLatency measures round-trip times to target by timing small HTTP
// requests over a persistent connection.
func (c *Client) MeasureLatency(ctx context.Context, target Server) (Latency, error) {
	return c.measureLatency(ctx, target, c.pingCount)
}

func (c *Client) measureLatency(ctx context.Context, target Server, count int) (Latency, error) {
	endpoint, err := target.endpoint("latency.txt")
	if err != nil {
		return Latency{}, err
	}

	// The first request pays for DNS, TCP, and possibly TLS setup, so it is
	// discarded.
	_, err = c.ping(ctx, endpoint)
	if err != nil {
		return Latency{}, fmt.Errorf("failed to measure latency: %w", err)
	}

	samples := make([]time.Duration, 0, count)

	for range count {
		sample, err := c.ping(ctx, endpoint)
		if err != nil {
			return Latency{}, fmt.Errorf("failed to measure latency: %w", err)
		}

		samples = append(samples, sample)
	}

	return summarizeLatency(samples), nil
}

func (c *Client) ping(ctx context.Context, endpoint string) (time.Duration, error) {
	request, err := c.newRequest(ctx, http.MethodGet, cacheBust(endpoint), nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()

	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}
	defer closeBody(response.Body)

	// Draining the body lets the connection be reused for the next sample.
	_, err = io.Copy(io.Discard, response.Body)
	elapsed := time.Since(start)

	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	err = checkStatus(response)
	if err != nil {
		return 0, err
	}

	return elapsed, nil
}

// summarizeLatency computes statistics over samples, which must not be
// empty.
func summarizeLatency(samples []time.Duration) Latency {
	var sum, jitterSum time.Duration

	previous := samples[0]

	for _, sample := range samples {
		sum += sample
		jitterSum += (sample - previous).Abs()
		previous = sample
	}

	result := Latency{
		Mean:    sum / time.Duration(len(samples)),
		Min:     slices.Min(samples),
		Max:     slices.Max(samples),
		Samples: samples,
	}

	if len(samples) > 1 {
		result.Jitter = jitterSum / time.Duration(len(samples)-1)
	}

	return result
}
