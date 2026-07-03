package main

import "github.com/prometheus/client_golang/prometheus"

const metricsNamespace = "speedtest"

type Metrics struct {
	downloadSpeed prometheus.Gauge
	uploadSpeed   prometheus.Gauge
	ping          prometheus.Gauge
	jitter        prometheus.Gauge
	resultValid   prometheus.Gauge
	testDuration  prometheus.Gauge
	up            prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	gauges := &Metrics{
		downloadSpeed: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "download_speed_bps",
			Help:      "Download speed (bit/s)",
		}),
		uploadSpeed: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "upload_speed_bps",
				Help:      "Upload speed (bit/s)",
			},
		),
		ping: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "ping_seconds",
				Help:      "Latency (seconds)",
			},
		),
		jitter: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "jitter_seconds",
				Help:      "Jitter (seconds)",
			},
		),
		resultValid: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "result_valid",
				Help:      "Indicates if the result is logical given UL and DL speed",
			},
		),
		testDuration: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "test_duration_seconds",
				Help:      "Duration of the test (seconds)",
			},
		),
		up: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: metricsNamespace,
				Name:      "up",
				Help:      "Indicates if the last speedtest was successful",
			},
		),
	}

	reg.MustRegister(gauges.downloadSpeed)
	reg.MustRegister(gauges.uploadSpeed)
	reg.MustRegister(gauges.ping)
	reg.MustRegister(gauges.jitter)
	reg.MustRegister(gauges.resultValid)
	reg.MustRegister(gauges.testDuration)
	reg.MustRegister(gauges.up)

	return gauges
}
