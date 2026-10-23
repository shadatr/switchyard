// Package metrics holds Switchyard's Prometheus collectors.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is the set of collectors, registered on a private registry.
type Metrics struct {
	Registry *prometheus.Registry

	Requests         *prometheus.CounterVec   // alias, target, status
	Duration         *prometheus.HistogramVec // alias, stream
	TTFT             *prometheus.HistogramVec // alias
	Overhead         prometheus.Histogram
	UpstreamErrors   *prometheus.CounterVec // target, code
	Attempts         prometheus.Histogram
	DependencyErrors *prometheus.CounterVec // dep
	RateLimited      *prometheus.CounterVec // kind (rpm|tpm|budget)
	Cache            *prometheus.CounterVec // result (hit|miss|bypass|store)
	BreakerState     *prometheus.GaugeVec   // target
	LogsDropped      prometheus.Counter
	LogsWritten      prometheus.Counter
	LiveSubscribers  prometheus.Gauge
}

// New creates and registers all collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	f := func(c prometheus.Collector) { reg.MustRegister(c) }

	m := &Metrics{Registry: reg}
	m.Requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "switchyard_requests_total", Help: "Chat requests by alias, target and HTTP status.",
	}, []string{"alias", "target", "status"})
	m.Duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "switchyard_request_duration_seconds", Help: "Total request latency.",
		Buckets: prometheus.ExponentialBuckets(0.005, 2, 14),
	}, []string{"alias", "stream"})
	m.TTFT = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "switchyard_ttft_seconds", Help: "Time to first token.",
		Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
	}, []string{"alias"})
	m.Overhead = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "switchyard_gateway_overhead_seconds", Help: "Request latency minus time spent waiting on the upstream.",
		Buckets: prometheus.ExponentialBuckets(0.00001, 2, 16), // 10µs … ~330ms
	})
	m.UpstreamErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "switchyard_upstream_errors_total", Help: "Failed upstream attempts.",
	}, []string{"target", "code"})
	m.Attempts = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "switchyard_upstream_attempts", Help: "Upstream attempts per request.", Buckets: []float64{1, 2, 3, 4, 5},
	})
	m.DependencyErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "switchyard_dependency_errors_total", Help: "Errors talking to Redis/Postgres/ClickHouse.",
	}, []string{"dep"})
	m.RateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "switchyard_rate_limited_total", Help: "Requests rejected by limits.",
	}, []string{"kind"})
	m.Cache = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "switchyard_cache_total", Help: "Response cache events.",
	}, []string{"result"})
	m.BreakerState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "switchyard_breaker_state", Help: "Circuit breaker state per target (0 closed, 1 half-open, 2 open).",
	}, []string{"target"})
	m.LogsDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "switchyard_logs_dropped_total", Help: "Request log records dropped because the queue was full or ClickHouse failed.",
	})
	m.LogsWritten = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "switchyard_logs_written_total", Help: "Request log records written to ClickHouse.",
	})
	m.LiveSubscribers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "switchyard_live_subscribers", Help: "Connected live-tail subscribers.",
	})
	for _, c := range []prometheus.Collector{m.Requests, m.Duration, m.TTFT, m.Overhead, m.UpstreamErrors, m.Attempts,
		m.DependencyErrors, m.RateLimited, m.Cache, m.BreakerState, m.LogsDropped, m.LogsWritten, m.LiveSubscribers} {
		f(c)
	}
	return m
}

// Handler serves the registry in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
