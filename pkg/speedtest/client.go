// Package speedtest measures latency and throughput against speedtest.net
// servers using their HTTP protocol. Unlike general-purpose clients it is
// tuned to saturate multi-gigabit links: it keeps one persistent HTTP/1.1
// connection per stream, transfers large payloads through large buffers, and
// can autotune the number of parallel streams during a warmup phase that is
// excluded from the measurement.
package speedtest

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"time"
)

// DefaultServerListURL is the speedtest.net endpoint listing the servers
// closest to the caller.
const DefaultServerListURL = "https://www.speedtest.net/api/js/servers"

// DefaultUserAgent is sent with every request unless overridden with
// WithUserAgent.
const DefaultUserAgent = "prometheus-speedtest-exporter"

// Defaults applied by New for options that are not set.
const (
	DefaultServerCandidates = 5
	DefaultPingCount        = 10
	DefaultDuration         = 10 * time.Second
	DefaultWarmup           = 4 * time.Second
	DefaultMaxStreams       = 32
	DefaultDownloadSize     = 4000
	DefaultUploadSize       = 16 << 20  // 16 MiB
	DefaultBufferSize       = 256 << 10 // 256 KiB
)

const (
	dialTimeout           = 10 * time.Second
	keepAlive             = 30 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
	idleConnTimeout       = 90 * time.Second
)

// downloadSizes are the dimensions of the randomNxN.jpg files every
// speedtest.net server provides; the largest is roughly 30 MB.
//
//nolint:gochecknoglobals // Read-only lookup table.
var downloadSizes = []int{350, 500, 750, 1000, 1500, 2000, 2500, 3000, 3500, 4000}

// ErrInvalidOption is returned by New when an option has an invalid value.
var ErrInvalidOption = errors.New("invalid option")

// Client runs speedtests. It is safe for concurrent use, although running
// several tests at once makes them compete for the same link.
type Client struct {
	httpClient       *http.Client
	userAgent        string
	serverListURL    string
	serverCandidates int
	pingCount        int
	duration         time.Duration
	warmup           time.Duration
	streams          int
	maxStreams       int
	downloadSize     int
	uploadSize       int64
	bufferSize       int

	// payload is the read-only pattern that upload bodies cycle through.
	payload []byte
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the tuned default HTTP client. The caller is then
// responsible for keeping enough idle connections per host for all streams.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.httpClient = httpClient }
}

// WithUserAgent sets the User-Agent header sent with every request.
func WithUserAgent(userAgent string) Option {
	return func(c *Client) { c.userAgent = userAgent }
}

// WithServerListURL overrides the endpoint servers are discovered from.
func WithServerListURL(serverListURL string) Option {
	return func(c *Client) { c.serverListURL = serverListURL }
}

// WithServerCandidates sets how many of the nearest servers are probed for
// latency before the fastest one is selected.
func WithServerCandidates(n int) Option {
	return func(c *Client) { c.serverCandidates = n }
}

// WithPingCount sets how many latency samples are taken.
func WithPingCount(n int) Option {
	return func(c *Client) { c.pingCount = n }
}

// WithDuration sets how long throughput is measured in each direction,
// excluding the warmup.
func WithDuration(duration time.Duration) Option {
	return func(c *Client) { c.duration = duration }
}

// WithWarmup sets how long each direction ramps up before measuring. Bytes
// transferred during warmup, while TCP slow start and stream autotuning are
// still converging, do not count towards the result.
func WithWarmup(warmup time.Duration) Option {
	return func(c *Client) { c.warmup = warmup }
}

// WithStreams fixes the number of parallel streams and disables autotuning.
func WithStreams(n int) Option {
	return func(c *Client) { c.streams = n }
}

// WithMaxStreams caps the number of parallel streams autotuning may open.
func WithMaxStreams(n int) Option {
	return func(c *Client) { c.maxStreams = n }
}

// WithDownloadSize selects which randomNxN.jpg file is downloaded. Valid
// sizes are 350, 500, 750, 1000, 1500, 2000, 2500, 3000, 3500, and 4000.
func WithDownloadSize(size int) Option {
	return func(c *Client) { c.downloadSize = size }
}

// WithUploadSize sets the size in bytes of each upload request body.
func WithUploadSize(size int64) Option {
	return func(c *Client) { c.uploadSize = size }
}

// WithBufferSize sets the size in bytes of the per-stream read buffer and of
// the transport's socket read and write buffers.
func WithBufferSize(size int) Option {
	return func(c *Client) { c.bufferSize = size }
}

// New creates a Client with the given options applied over the defaults.
func New(opts ...Option) (*Client, error) {
	client := &Client{
		userAgent:        DefaultUserAgent,
		serverListURL:    DefaultServerListURL,
		serverCandidates: DefaultServerCandidates,
		pingCount:        DefaultPingCount,
		duration:         DefaultDuration,
		warmup:           DefaultWarmup,
		maxStreams:       DefaultMaxStreams,
		downloadSize:     DefaultDownloadSize,
		uploadSize:       DefaultUploadSize,
		bufferSize:       DefaultBufferSize,
	}

	for _, opt := range opts {
		opt(client)
	}

	err := client.validate()
	if err != nil {
		return nil, err
	}

	if client.httpClient == nil {
		client.httpClient = &http.Client{Transport: newTransport(client.streamLimit(), client.bufferSize)}
	}

	client.payload = make([]byte, client.bufferSize)
	// Random data keeps compressing middleboxes from inflating upload
	// results. crypto/rand.Read never returns an error.
	_, _ = rand.Read(client.payload)

	return client, nil
}

func (c *Client) validate() error {
	checks := []struct {
		ok      bool
		message string
		value   any
	}{
		{c.serverCandidates > 0, "server candidates must be positive", c.serverCandidates},
		{c.pingCount > 0, "ping count must be positive", c.pingCount},
		{c.duration > 0, "duration must be positive", c.duration},
		{c.warmup >= 0, "warmup must not be negative", c.warmup},
		{c.streams >= 0, "streams must not be negative", c.streams},
		{c.maxStreams > 0, "max streams must be positive", c.maxStreams},
		{
			slices.Contains(downloadSizes, c.downloadSize),
			fmt.Sprintf("download size must be one of %v", downloadSizes),
			c.downloadSize,
		},
		{c.uploadSize > 0, "upload size must be positive", c.uploadSize},
		{c.bufferSize > 0, "buffer size must be positive", c.bufferSize},
	}

	var errs []error

	for _, check := range checks {
		if !check.ok {
			errs = append(errs, fmt.Errorf("%w: %s, got %v", ErrInvalidOption, check.message, check.value))
		}
	}

	return errors.Join(errs...)
}

// streamLimit returns the highest number of streams a test may open.
func (c *Client) streamLimit() int {
	if c.streams > 0 {
		return c.streams
	}

	return c.maxStreams
}

// newTransport returns an HTTP/1.1 transport that keeps an idle connection
// for every stream, so each stream reuses one warmed-up TCP connection
// instead of restarting slow start on every request. HTTP/2 is disabled
// because it would multiplex all streams onto a single TCP connection.
func newTransport(streams, bufferSize int) *http.Transport {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}

	var protocols http.Protocols
	protocols.SetHTTP1(true)

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		Protocols:             &protocols,
		MaxIdleConns:          streams,
		MaxIdleConnsPerHost:   streams,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		DisableCompression:    true,
		ReadBufferSize:        bufferSize,
		WriteBufferSize:       bufferSize,
	}
}
