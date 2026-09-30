package speedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	bitsPerByte = 8

	// initialStreams is how many streams autotuning starts with.
	initialStreams = 4
	// rampSteps is how many times autotuning re-evaluates the stream count
	// during warmup.
	rampSteps = 8
	// growthThreshold is the relative throughput gain an added batch of
	// streams must yield for autotuning to keep adding streams.
	growthThreshold = 0.1
	// retryBackoff is how long a stream waits after a failed request.
	retryBackoff = 100 * time.Millisecond
)

// ErrNoData is returned when a throughput test transferred no data during
// its measurement window.
var ErrNoData = errors.New("no data transferred")

// Throughput is the result of measuring one direction.
type Throughput struct {
	// BitsPerSecond is the throughput over the measurement window.
	BitsPerSecond float64
	// Bytes is the amount of data transferred during the measurement
	// window, excluding warmup.
	Bytes int64
	// Duration is the length of the measurement window.
	Duration time.Duration
	// Streams is the number of parallel streams used for the measurement.
	Streams int
}

// transferFunc performs one request, adding every transferred byte to
// counter. buf is a scratch buffer owned by the calling stream.
type transferFunc func(ctx context.Context, counter *atomic.Int64, buf []byte) error

// MeasureDownload measures download throughput from target.
func (c *Client) MeasureDownload(ctx context.Context, target Server) (Throughput, error) {
	endpoint, err := target.endpoint("random" + strconv.Itoa(c.downloadSize) + "x" + strconv.Itoa(c.downloadSize) + ".jpg")
	if err != nil {
		return Throughput{}, err
	}

	result, err := c.measure(ctx, func(ctx context.Context, counter *atomic.Int64, buf []byte) error {
		return c.download(ctx, endpoint, counter, buf)
	})
	if err != nil {
		return Throughput{}, fmt.Errorf("failed to measure download: %w", err)
	}

	return result, nil
}

// MeasureUpload measures upload throughput to target.
func (c *Client) MeasureUpload(ctx context.Context, target Server) (Throughput, error) {
	result, err := c.measure(ctx, func(ctx context.Context, counter *atomic.Int64, _ []byte) error {
		return c.upload(ctx, target.URL, counter)
	})
	if err != nil {
		return Throughput{}, fmt.Errorf("failed to measure upload: %w", err)
	}

	return result, nil
}

func (c *Client) download(ctx context.Context, endpoint string, counter *atomic.Int64, buf []byte) error {
	request, err := c.newRequest(ctx, http.MethodGet, cacheBust(endpoint), nil)
	if err != nil {
		return err
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer closeBody(response.Body)

	err = checkStatus(response)
	if err != nil {
		return err
	}

	for {
		n, err := response.Body.Read(buf)
		counter.Add(int64(n))

		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}
	}
}

func (c *Client) upload(ctx context.Context, endpoint string, counter *atomic.Int64) error {
	body := &payloadReader{payload: c.payload, remaining: c.uploadSize, counter: counter}

	request, err := c.newRequest(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}

	request.ContentLength = c.uploadSize
	request.Header.Set("Content-Type", "application/octet-stream")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer closeBody(response.Body)

	// Draining the body lets the connection be reused for the next request.
	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	return checkStatus(response)
}

// measure runs transfer on parallel streams until the warmup and the
// measurement window have elapsed, and reports the throughput achieved
// during the measurement window only.
func (c *Client) measure(parent context.Context, transfer transferFunc) (Throughput, error) {
	ctx, cancel := context.WithCancel(parent)

	var (
		counter  atomic.Int64
		group    sync.WaitGroup
		firstErr error
		errOnce  sync.Once
		streams  int
	)

	defer func() {
		cancel()
		group.Wait()
	}()

	spawn := func(count int) {
		for range count {
			streams++

			group.Go(func() {
				err := c.runStream(ctx, transfer, &counter)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
				}
			})
		}
	}

	if c.streams > 0 {
		spawn(c.streams)
		sleep(ctx, c.warmup)
	} else {
		spawn(min(initialStreams, c.maxStreams))
		c.autotune(ctx, &counter, &streams, spawn)
	}

	startBytes, start := counter.Load(), time.Now()

	sleep(ctx, c.duration)

	transferred, elapsed := counter.Load()-startBytes, time.Since(start)

	if parent.Err() != nil {
		return Throughput{}, fmt.Errorf("test aborted: %w", parent.Err())
	}

	cancel()
	group.Wait()

	if transferred == 0 {
		return Throughput{}, errors.Join(ErrNoData, firstErr)
	}

	return Throughput{
		BitsPerSecond: float64(transferred*bitsPerByte) / elapsed.Seconds(),
		Bytes:         transferred,
		Duration:      elapsed,
		Streams:       streams,
	}, nil
}

// autotune grows the number of streams during warmup. It doubles the stream
// count as long as the previous doubling raised throughput by at least
// growthThreshold, and stops once the link appears saturated or the stream
// limit is reached.
func (c *Client) autotune(ctx context.Context, counter *atomic.Int64, streams *int, spawn func(int)) {
	interval := c.warmup / rampSteps
	growing := true

	var previousRate int64

	for range rampSteps {
		before := counter.Load()

		if !sleep(ctx, interval) {
			return
		}

		rate := counter.Load() - before

		if growing && previousRate > 0 {
			growing = float64(rate) >= float64(previousRate)*(1+growthThreshold)
			if growing && *streams < c.maxStreams {
				spawn(min(*streams, c.maxStreams-*streams))
			}
		}

		previousRate = rate
	}
}

// runStream repeatedly performs transfer until ctx is canceled. Failed
// requests are retried after a short backoff; the most recent failure is
// returned if ctx was canceled while the stream was failing.
func (c *Client) runStream(ctx context.Context, transfer transferFunc, counter *atomic.Int64) error {
	buf := make([]byte, c.bufferSize)

	var lastErr error

	for ctx.Err() == nil {
		err := transfer(ctx, counter, buf)
		if err == nil || ctx.Err() != nil {
			continue
		}

		lastErr = err

		sleep(ctx, retryBackoff)
	}

	return lastErr
}

// sleep waits for duration or until ctx is canceled, and reports whether
// the full duration elapsed.
func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// payloadReader yields remaining bytes by cycling through payload, adding
// every byte read to counter.
type payloadReader struct {
	payload   []byte
	offset    int
	remaining int64
	counter   *atomic.Int64
}

func (r *payloadReader) Read(buf []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}

	if int64(len(buf)) > r.remaining {
		buf = buf[:r.remaining]
	}

	n := copy(buf, r.payload[r.offset:])
	r.offset = (r.offset + n) % len(r.payload)
	r.remaining -= int64(n)
	r.counter.Add(int64(n))

	return n, nil
}
