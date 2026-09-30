package speedtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// probeCount is how many latency samples are taken per candidate when
// selecting a server.
const probeCount = 3

var (
	// ErrNoServers is returned when no usable server could be found.
	ErrNoServers = errors.New("no available server found")
	// ErrUnexpectedStatus is returned when a server responds with a
	// non-200 status code.
	ErrUnexpectedStatus = errors.New("unexpected status")
)

// Server is a speedtest.net server.
type Server struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
	Sponsor string `json:"sponsor"`
	Host    string `json:"host"`
	// URL is the server's upload endpoint, usually ending in /upload.php.
	// The other endpoints live next to it.
	URL string `json:"url"`
	// Distance is the distance to the caller in kilometers, as estimated by
	// speedtest.net from the caller's IP address.
	Distance float64 `json:"distance"`
}

// endpoint resolves a file next to the server's upload endpoint.
func (s Server) endpoint(name string) (string, error) {
	base, err := url.Parse(s.URL)
	if err != nil {
		return "", fmt.Errorf("failed to parse server URL: %w", err)
	}

	return base.JoinPath("..", name).String(), nil
}

// FetchServers returns the servers nearest to the caller, as reported by
// speedtest.net, limited to the configured number of candidates.
func (c *Client) FetchServers(ctx context.Context) ([]Server, error) {
	listURL, err := url.Parse(c.serverListURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server list URL: %w", err)
	}

	query := listURL.Query()
	query.Set("engine", "js")
	query.Set("limit", strconv.Itoa(c.serverCandidates))
	listURL.RawQuery = query.Encode()

	request, err := c.newRequest(ctx, http.MethodGet, listURL.String(), nil)
	if err != nil {
		return nil, err
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch server list: %w", err)
	}
	defer closeBody(response.Body)

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch server list: %w: %s", ErrUnexpectedStatus, response.Status)
	}

	var servers []Server

	err = json.NewDecoder(response.Body).Decode(&servers)
	if err != nil {
		return nil, fmt.Errorf("failed to decode server list: %w", err)
	}

	return servers, nil
}

// SelectServer fetches the nearest servers and returns the one with the
// lowest latency. Servers that fail to respond are skipped.
func (c *Client) SelectServer(ctx context.Context) (Server, error) {
	servers, err := c.FetchServers(ctx)
	if err != nil {
		return Server{}, err
	}

	latencies := make([]time.Duration, len(servers))
	errs := make([]error, len(servers))

	var group sync.WaitGroup

	for index, server := range servers {
		group.Go(func() {
			result, err := c.measureLatency(ctx, server, probeCount)
			latencies[index], errs[index] = result.Mean, err
		})
	}

	group.Wait()

	best := -1

	for index := range servers {
		if errs[index] == nil && (best < 0 || latencies[index] < latencies[best]) {
			best = index
		}
	}

	if best < 0 {
		return Server{}, errors.Join(append([]error{ErrNoServers}, errs...)...)
	}

	return servers[best], nil
}

// newRequest builds a request carrying the configured User-Agent.
func (c *Client) newRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	request.Header.Set("User-Agent", c.userAgent)

	return request, nil
}

// cacheBust appends a unique query parameter so that caching proxies never
// answer in place of the server.
func cacheBust(target string) string {
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}

	return target + separator + "nocache=" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// closeBody closes a response body. Close errors are irrelevant once the
// body has been read or abandoned.
func closeBody(body io.Closer) {
	_ = body.Close()
}

// checkStatus returns an error if response is not a 200 OK.
func checkStatus(response *http.Response) error {
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
	}

	return nil
}
