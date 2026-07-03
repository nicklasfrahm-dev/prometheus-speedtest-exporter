package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/showwin/speedtest-go/speedtest"
)

var errNoServerFound = errors.New("no available server found")

// findNearestServer resolves the speedtest.net server with the lowest
// network distance to the current host.
func findNearestServer(ctx context.Context, client *speedtest.Speedtest) (*speedtest.Server, error) {
	serverList, err := client.FetchServerListContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch server list: %w", err)
	}

	targets, err := serverList.Available().FindServer([]int{})
	if len(targets) == 0 {
		err = errNoServerFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to find available server: %w", err)
	}

	minDistance := math.MaxFloat64

	var target *speedtest.Server

	for _, candidate := range targets {
		if candidate.Distance < minDistance {
			minDistance = candidate.Distance
			target = candidate
		}
	}

	return target, nil
}

// runFullSpeedtest runs the ping, download, and upload tests against the
// nearest available server, observing ctx for cancellation and timeout.
func runFullSpeedtest(ctx context.Context) (*speedtest.Server, time.Duration, error) {
	client := speedtest.New()

	target, err := findNearestServer(ctx, client)
	if err != nil {
		return nil, 0, err
	}

	start := time.Now()

	err = target.PingTestContext(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to run ping test: %w", err)
	}

	err = target.DownloadTestContext(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to run download test: %w", err)
	}

	err = target.UploadTestContext(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to run upload test: %w", err)
	}

	return target, time.Since(start), nil
}
