package speedtest

import (
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeLatency(t *testing.T) {
	t.Parallel()

	samples := []time.Duration{10 * time.Millisecond, 14 * time.Millisecond, 12 * time.Millisecond, 20 * time.Millisecond}

	got := summarizeLatency(samples)

	assert.Equal(t, 14*time.Millisecond, got.Mean)
	assert.Equal(t, 10*time.Millisecond, got.Min)
	assert.Equal(t, 20*time.Millisecond, got.Max)
	// |14-10| + |12-14| + |20-12| = 14, over 3 differences.
	assert.Equal(t, 14*time.Millisecond/3, got.Jitter)
}

func TestSummarizeLatencySingleSample(t *testing.T) {
	t.Parallel()

	got := summarizeLatency([]time.Duration{time.Millisecond})

	assert.Equal(t, time.Millisecond, got.Mean)
	assert.Zero(t, got.Jitter)
}

func TestPayloadReader(t *testing.T) {
	t.Parallel()

	var counter atomic.Int64

	reader := &payloadReader{payload: []byte("abc"), remaining: 8, counter: &counter}

	got, err := io.ReadAll(reader)
	require.NoError(t, err)

	assert.Equal(t, "abcabcab", string(got))
	assert.Equal(t, int64(8), counter.Load())
}

func TestServerEndpoint(t *testing.T) {
	t.Parallel()

	server := Server{URL: "http://example.com:8080/speedtest/upload.php"}

	got, err := server.endpoint("latency.txt")
	require.NoError(t, err)

	assert.Equal(t, "http://example.com:8080/speedtest/latency.txt", got)
}

func TestCacheBust(t *testing.T) {
	t.Parallel()

	assert.Regexp(t, `^http://a/b\?nocache=\d+$`, cacheBust("http://a/b"))
	assert.Regexp(t, `^http://a/b\?x=1&nocache=\d+$`, cacheBust("http://a/b?x=1"))
}
