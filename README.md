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
docker run --rm -p 9516:9516 ghcr.io/nicklasfrahm/prometheus-speedtest-exporter:latest
```

### Configuration

| Environment variable | Default | Supported values                    | Description                                     |
| --------------------- | ------- | ------------------------------------ | ------------------------------------------------ |
| `PORT`                | `9516`  | any valid port                       | Port the HTTP server listens on                  |
| `LOG_LEVEL`           | `warn`  | `debug`, `info`, `warn`/`warning`, `error` (case insensitive) | Minimum log level emitted |
| `LOG_FORMAT`          | `json`  | `json`, `console`/`text` (case insensitive) | Log output format; `console`/`text` renders color-coded levels via [tint][tint] |
| `SCRAPE_INTERVAL`     | `1h`    | any [`time.ParseDuration`][go-duration] value | Minimum time between real speedtests; scrapes within this window are served from cache |
| `SCRAPE_TIMEOUT`      | `5m`    | any [`time.ParseDuration`][go-duration] value | Maximum time a single speedtest is allowed to run before it's cancelled |

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
[tint]: https://github.com/lmittmann/tint
[go-duration]: https://pkg.go.dev/time#ParseDuration
[semantic-release]: https://github.com/semantic-release/semantic-release
[conventional-commits]: https://www.conventionalcommits.org/
