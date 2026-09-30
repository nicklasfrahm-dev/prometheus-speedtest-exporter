# Prometheus Speedtest Exporter

This is a prometheus speedtest exporter written purely in [Golang][golang]. It uses the default port for the speedtest exporter `9516`.

## Usage

```bash
./bin/prometheus-speedtest-exporter
{"time":"2026-07-02T20:00:14+02:00","level":"INFO","msg":"Starting application","application":"prometheus-speedtest-exporter","version":"v0.1.0"}
{"time":"2026-07-02T20:00:14+02:00","level":"INFO","msg":"Starting server","address":"http://0.0.0.0:9516"}
```

A container image is published on every release:

```bash
docker run --rm -p 9516:9516 ghcr.io/nicklasfrahm-dev/prometheus-speedtest-exporter:latest
```

### Configuration

| Environment variable | Default | Supported values                    | Description                                     |
| --------------------- | ------- | ------------------------------------ | ------------------------------------------------ |
| `PORT`                | `9516`  | any valid port                       | Port the HTTP server listens on                  |
| `LOG_LEVEL`           | `warn`  | `debug`, `info`, `warn`/`warning`, `error` (case insensitive) | Minimum log level emitted |
| `LOG_FORMAT`          | `json`  | `json`, `console`/`text` (case insensitive) | Log output format; `console`/`text` renders color-coded levels via [tint][tint] |
| `SCRAPE_INTERVAL`     | `1h`    | any [`time.ParseDuration`][go-duration] value | Minimum time between real speedtests; scrapes within this window are served from cache |
| `SCRAPE_TIMEOUT`      | `5m`    | any [`time.ParseDuration`][go-duration] value | Maximum time a single speedtest is allowed to run before it's cancelled |

### Measurement

Speedtests are run by the [`pkg/speedtest`](pkg/speedtest) library against [speedtest.net][speedtest] servers. It probes the nearest servers and picks the one with the lowest latency. It then measures latency (10 HTTP round trips; jitter is the mean difference between consecutive samples) and runs a download and an upload test of 10 seconds each.

To saturate multi-gigabit links, each throughput test transfers large payloads over parallel persistent HTTP/1.1 connections. A 4-second warmup, excluded from the result, lets TCP slow start converge. During warmup the library autotunes the number of parallel streams: it starts with 4 and keeps doubling, up to 32, as long as each doubling raises throughput by at least 10%.

The defaults saturate links of 1 Gbit/s and more without further tuning. All of them can be overridden with the following environment variables. Invalid values are rejected at startup instead of being silently ignored, except for values that fail to parse, which log a warning and fall back to the default.

| Environment variable          | Default  | Supported values | Description |
| ----------------------------- | -------- | ---------------- | ----------- |
| `SPEEDTEST_SERVER_CANDIDATES` | `5`      | positive integer | Number of nearest servers probed for latency; the fastest is used for the test |
| `SPEEDTEST_PING_COUNT`        | `10`     | positive integer | Number of latency samples taken |
| `SPEEDTEST_DURATION`          | `10s`    | positive [`time.ParseDuration`][go-duration] value | Measurement window per direction, excluding warmup |
| `SPEEDTEST_WARMUP`            | `4s`     | non-negative [`time.ParseDuration`][go-duration] value | Ramp-up per direction that is excluded from the result; stream autotuning happens during this phase |
| `SPEEDTEST_STREAMS`           | `0`      | non-negative integer | Fixed number of parallel streams; `0` autotunes the stream count |
| `SPEEDTEST_MAX_STREAMS`       | `32`     | positive integer | Upper bound for autotuned streams; raise it for links above ~10 Gbit/s or with high latency |
| `SPEEDTEST_DOWNLOAD_SIZE`     | `4000`   | `350`, `500`, `750`, `1000`, `1500`, `2000`, `2500`, `3000`, `3500`, `4000` | Downloaded test file (`randomNxN.jpg`); `4000` is roughly 30 MB per request |
| `SPEEDTEST_UPLOAD_SIZE`       | `16MiB`  | positive size in bytes, optionally suffixed with `B`, `KiB`, `MiB`, or `GiB` | Body size of each upload request |
| `SPEEDTEST_BUFFER_SIZE`       | `256KiB` | positive size in bytes, optionally suffixed with `B`, `KiB`, `MiB`, or `GiB` | Per-stream read buffer and socket read/write buffer size |

Each run takes roughly `2 × (SPEEDTEST_WARMUP + SPEEDTEST_DURATION)` plus a few seconds for server selection and latency, so keep `SCRAPE_TIMEOUT` well above that when increasing the durations.

### Caching

`/metrics` always responds immediately from a cache. The first scrape after the cache turns older than `SCRAPE_INTERVAL` triggers a new speedtest in the background (stale-while-revalidate); that scrape, and every one after it until the new result lands, still gets the last known values. This decouples how often your scraper polls `/metrics` from how often a real speedtest actually runs, so a short Prometheus/Alloy `scrape_interval` no longer triggers redundant tests or timeouts.

### Health checks

| Endpoint  | Meaning                                                                 |
| --------- | ------------------------------------------------------------------------ |
| `/livez`  | Process is up and serving requests                                       |
| `/readyz` | The cache holds at least one completed speedtest attempt (success or failure) |

### Sample metrics

The first scrape after startup (or after the cache goes stale) triggers a real speedtest in the background; that test typically takes 20-30s to complete, after which `/metrics` reflects the new result:

```text
# HELP speedtest_download_speed_bps Download speed (bit/s)
# TYPE speedtest_download_speed_bps gauge
speedtest_download_speed_bps 1.34576e+07
# HELP speedtest_jitter_seconds Jitter (seconds)
# TYPE speedtest_jitter_seconds gauge
speedtest_jitter_seconds 0.012366775
# HELP speedtest_ping_seconds Latency (seconds)
# TYPE speedtest_ping_seconds gauge
speedtest_ping_seconds 0.063418353
# HELP speedtest_result_valid Indicates if the result is logical given UL and DL speed
# TYPE speedtest_result_valid gauge
speedtest_result_valid 1
# HELP speedtest_test_duration_seconds Duration of the test (seconds)
# TYPE speedtest_test_duration_seconds gauge
speedtest_test_duration_seconds 23.27538563
# HELP speedtest_up Indicates if the last speedtest was successful
# TYPE speedtest_up gauge
speedtest_up 1
# HELP speedtest_upload_speed_bps Upload speed (bit/s)
# TYPE speedtest_upload_speed_bps gauge
speedtest_upload_speed_bps 2.9622e+06
```

### Prometheus configuration

Since `/metrics` now always serves from cache, `scrape_interval`/`scrape_timeout` only need to cover the HTTP round trip, not a full speedtest:

```yaml
scrape_configs:
  - job_name: speedtest
    metrics_path: /metrics
    scrape_interval: 30s
    scrape_timeout: 10s
    static_configs:
      - targets:
          - localhost:9516
```

## Releases

Releases are fully automated via [semantic-release][semantic-release] based on [Conventional Commits][conventional-commits]. On every push to `main`, [`.github/workflows/release.yml`](.github/workflows/release.yml):

1. Determines the next version from commit messages and, if a release is warranted, creates a Git tag and GitHub release.
2. Builds and pushes a multi-arch (`linux/amd64`, `linux/arm64`) container image to `ghcr.io`, tagged `latest` and with the release version.

## Related projects

Why another prometheus speedtest exporter? The container image is less than `10MB` in size! I am planning to use this exporter for Kubernetes at the network edge, hence every MB counts.

- [jeanralphaviles/prometheus_speedtest (Python)](https://github.com/jeanralphaviles/prometheus_speedtest)
- [billimek/prometheus-speedtest-exporter (Shell)](https://github.com/billimek/prometheus-speedtest-exporter)
- [danopstech/speedtest_exporter (Python)](https://github.com/danopstech/speedtest_exporter)

## License

This project is licensed under the terms of the [MIT license](./LICENSE.md).

[golang]: https://go.dev/
[speedtest]: https://www.speedtest.net/
[tint]: https://github.com/lmittmann/tint
[go-duration]: https://pkg.go.dev/time#ParseDuration
[semantic-release]: https://github.com/semantic-release/semantic-release
[conventional-commits]: https://www.conventionalcommits.org/
