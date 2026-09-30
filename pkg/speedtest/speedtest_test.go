package speedtest_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nicklasfrahm-dev/prometheus-speedtest-exporter/pkg/speedtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDownloadBytes = 1 << 20
	testUploadSize    = 256 << 10
)

// fakeConfig controls how a fakeServer behaves.
type fakeConfig struct {
	latencyDelay time.Duration
	failRequests bool
}

// fakeServer mimics the endpoints of a speedtest.net server.
type fakeServer struct {
	*httptest.Server
	fakeConfig

	uploaded  atomic.Int64
	userAgent atomic.Value
}

func newFakeServer(t *testing.T, config fakeConfig) *fakeServer {
	t.Helper()

	fake := &fakeServer{fakeConfig: config}

	mux := http.NewServeMux()
	mux.HandleFunc("/speedtest/latency.txt", func(writer http.ResponseWriter, request *http.Request) {
		fake.userAgent.Store(request.UserAgent())
		time.Sleep(fake.latencyDelay)

		_, _ = io.WriteString(writer, "test=test")
	})
	mux.HandleFunc("/speedtest/random350x350.jpg", func(writer http.ResponseWriter, _ *http.Request) {
		if fake.failRequests {
			writer.WriteHeader(http.StatusInternalServerError)

			return
		}

		writer.Header().Set("Content-Length", strconv.Itoa(testDownloadBytes))
		_, _ = writer.Write(make([]byte, testDownloadBytes))
	})
	mux.HandleFunc("/speedtest/upload.php", func(writer http.ResponseWriter, request *http.Request) {
		n, _ := io.Copy(io.Discard, request.Body)
		fake.uploaded.Add(n)
		_, _ = io.WriteString(writer, "size="+strconv.FormatInt(n, 10))
	})

	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)

	return fake
}

func (f *fakeServer) server(id string) speedtest.Server {
	return speedtest.Server{ID: id, URL: f.URL + "/speedtest/upload.php"}
}

// newServerList serves servers as a speedtest.net server list and records
// the limit query parameter of the last request.
func newServerList(t *testing.T, servers []speedtest.Server, limit *atomic.Value) *httptest.Server {
	t.Helper()

	list := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if limit != nil {
			limit.Store(request.URL.Query().Get("limit"))
		}

		_ = json.NewEncoder(writer).Encode(servers)
	}))
	t.Cleanup(list.Close)

	return list
}

func newTestClient(t *testing.T, opts ...speedtest.Option) *speedtest.Client {
	t.Helper()

	defaults := []speedtest.Option{
		speedtest.WithDuration(200 * time.Millisecond),
		speedtest.WithWarmup(100 * time.Millisecond),
		speedtest.WithDownloadSize(350),
		speedtest.WithUploadSize(testUploadSize),
		speedtest.WithBufferSize(32 << 10),
		speedtest.WithPingCount(3),
	}

	client, err := speedtest.New(append(defaults, opts...)...)
	require.NoError(t, err)

	return client
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	cases := map[string]speedtest.Option{
		"server candidates": speedtest.WithServerCandidates(0),
		"ping count":        speedtest.WithPingCount(0),
		"duration":          speedtest.WithDuration(0),
		"warmup":            speedtest.WithWarmup(-time.Second),
		"streams":           speedtest.WithStreams(-1),
		"max streams":       speedtest.WithMaxStreams(0),
		"download size":     speedtest.WithDownloadSize(1234),
		"upload size":       speedtest.WithUploadSize(0),
		"buffer size":       speedtest.WithBufferSize(0),
	}

	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client, err := speedtest.New(opt)
			require.ErrorIs(t, err, speedtest.ErrInvalidOption)
			assert.Nil(t, client)
		})
	}
}

func TestNewAcceptsDefaults(t *testing.T) {
	t.Parallel()

	client, err := speedtest.New()
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestFetchServers(t *testing.T) {
	t.Parallel()

	want := []speedtest.Server{
		{
			ID:       "1",
			Name:     "Aalborg",
			Country:  "Denmark",
			Sponsor:  "Example",
			Host:     "a:8080",
			URL:      "http://a:8080/speedtest/upload.php",
			Distance: 33,
		},
		{ID: "2", URL: "http://b:8080/speedtest/upload.php", Distance: 134},
	}

	var limit atomic.Value

	list := newServerList(t, want, &limit)
	client := newTestClient(t, speedtest.WithServerListURL(list.URL), speedtest.WithServerCandidates(7))

	got, err := client.FetchServers(t.Context())
	require.NoError(t, err)

	assert.Equal(t, want, got)
	assert.Equal(t, "7", limit.Load())
}

func TestFetchServersRejectsErrorStatus(t *testing.T) {
	t.Parallel()

	list := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(list.Close)

	client := newTestClient(t, speedtest.WithServerListURL(list.URL))

	_, err := client.FetchServers(t.Context())
	require.ErrorIs(t, err, speedtest.ErrUnexpectedStatus)
}

func TestSelectServerPicksLowestLatency(t *testing.T) {
	t.Parallel()

	slow := newFakeServer(t, fakeConfig{latencyDelay: 50 * time.Millisecond})
	fast := newFakeServer(t, fakeConfig{})

	list := newServerList(t, []speedtest.Server{
		slow.server("slow"),
		{ID: "unreachable", URL: "http://127.0.0.1:1/speedtest/upload.php"},
		fast.server("fast"),
	}, nil)
	client := newTestClient(t, speedtest.WithServerListURL(list.URL))

	got, err := client.SelectServer(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "fast", got.ID)
}

func TestSelectServerFailsWithoutServers(t *testing.T) {
	t.Parallel()

	list := newServerList(t, []speedtest.Server{}, nil)
	client := newTestClient(t, speedtest.WithServerListURL(list.URL))

	_, err := client.SelectServer(t.Context())
	require.ErrorIs(t, err, speedtest.ErrNoServers)
}

func TestMeasureLatency(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, fakeConfig{latencyDelay: 5 * time.Millisecond})

	client := newTestClient(t, speedtest.WithPingCount(4), speedtest.WithUserAgent("test-agent"))

	got, err := client.MeasureLatency(t.Context(), fake.server("1"))
	require.NoError(t, err)

	assert.Len(t, got.Samples, 4)
	assert.GreaterOrEqual(t, got.Min, fake.latencyDelay)
	assert.LessOrEqual(t, got.Min, got.Mean)
	assert.LessOrEqual(t, got.Mean, got.Max)
	assert.Equal(t, "test-agent", fake.userAgent.Load())
}

func TestMeasureDownload(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		opts        []speedtest.Option
		wantStreams func(t *testing.T, streams int)
	}{
		"fixed streams": {
			opts: []speedtest.Option{speedtest.WithStreams(3)},
			wantStreams: func(t *testing.T, streams int) {
				t.Helper()
				assert.Equal(t, 3, streams)
			},
		},
		"autotuned streams": {
			opts: []speedtest.Option{speedtest.WithMaxStreams(6)},
			wantStreams: func(t *testing.T, streams int) {
				t.Helper()
				assert.GreaterOrEqual(t, streams, 4)
				assert.LessOrEqual(t, streams, 6)
			},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeServer(t, fakeConfig{})
			client := newTestClient(t, testCase.opts...)

			got, err := client.MeasureDownload(t.Context(), fake.server("1"))
			require.NoError(t, err)

			assert.Positive(t, got.Bytes)
			assert.Positive(t, got.BitsPerSecond)
			assert.GreaterOrEqual(t, got.Duration, 200*time.Millisecond)
			testCase.wantStreams(t, got.Streams)
		})
	}
}

func TestMeasureDownloadReportsServerErrors(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, fakeConfig{failRequests: true})

	client := newTestClient(t, speedtest.WithStreams(1))

	_, err := client.MeasureDownload(t.Context(), fake.server("1"))
	require.ErrorIs(t, err, speedtest.ErrNoData)
	require.ErrorIs(t, err, speedtest.ErrUnexpectedStatus)
}

func TestMeasureUpload(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, fakeConfig{})
	client := newTestClient(t, speedtest.WithStreams(2))

	got, err := client.MeasureUpload(t.Context(), fake.server("1"))
	require.NoError(t, err)

	assert.Positive(t, got.Bytes)
	assert.Positive(t, got.BitsPerSecond)
	assert.Positive(t, fake.uploaded.Load())
}

func TestMeasureDownloadObservesCancellation(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, fakeConfig{})
	client := newTestClient(t, speedtest.WithDuration(time.Hour))

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	_, err := client.MeasureDownload(ctx, fake.server("1"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRun(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, fakeConfig{})
	list := newServerList(t, []speedtest.Server{fake.server("1")}, nil)
	client := newTestClient(t, speedtest.WithServerListURL(list.URL), speedtest.WithStreams(2))

	got, err := client.Run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, "1", got.Server.ID)
	assert.Len(t, got.Latency.Samples, 3)
	assert.Positive(t, got.Download.BitsPerSecond)
	assert.Positive(t, got.Upload.BitsPerSecond)
	assert.GreaterOrEqual(t, got.Duration, got.Download.Duration+got.Upload.Duration)
	assert.True(t, got.Valid())
}

func TestResultValid(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		down, up float64
		want     bool
	}{
		"symmetric":         {down: 1e9, up: 1e9, want: true},
		"asymmetric":        {down: 1e9, up: 5e7, want: true},
		"too asymmetric":    {down: 1e9, up: 1e6, want: false},
		"upload dominates":  {down: 1e6, up: 1e9, want: false},
		"no download":       {down: 0, up: 1e6, want: false},
		"no upload":         {down: 1e6, up: 0, want: false},
		"exact asymmetry":   {down: 1e8, up: 1e6, want: true},
		"nothing measured":  {down: 0, up: 0, want: false},
		"upload asymmetric": {down: 5e7, up: 1e9, want: true},
	}

	for name, testCase := range cases {
		result := &speedtest.Result{
			Download: speedtest.Throughput{BitsPerSecond: testCase.down},
			Upload:   speedtest.Throughput{BitsPerSecond: testCase.up},
		}

		assert.Equalf(t, testCase.want, result.Valid(), "case %q", name)
	}
}
