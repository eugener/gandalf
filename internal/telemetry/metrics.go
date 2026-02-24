// Package telemetry provides observability primitives for the Gandalf gateway.
package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all Prometheus collectors for the gateway.
type Metrics struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	ActiveRequests   prometheus.Gauge
	UpstreamDuration *prometheus.HistogramVec
	UpstreamErrors   *prometheus.CounterVec
	CacheHits        prometheus.Counter
	CacheMisses      prometheus.Counter
	RateLimitRejects *prometheus.CounterVec
	TokensProcessed  *prometheus.CounterVec
	UsageQueueLength prometheus.Gauge
}

// NewMetrics creates and registers all metrics with the given registerer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gandalf",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests.",
		}, []string{"method", "path", "status"}),

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:                       "gandalf",
			Name:                            "request_duration_seconds",
			Help:                            "HTTP request duration in seconds.",
			NativeHistogramBucketFactor:     1.1,
			NativeHistogramMaxBucketNumber:  100,
			NativeHistogramMinResetDuration: 0,
		}, []string{"method", "path"}),

		ActiveRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "gandalf",
			Name:      "active_requests",
			Help:      "Number of currently active requests.",
		}),

		UpstreamDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:                       "gandalf",
			Name:                            "upstream_duration_seconds",
			Help:                            "Upstream provider call duration in seconds.",
			NativeHistogramBucketFactor:     1.1,
			NativeHistogramMaxBucketNumber:  100,
			NativeHistogramMinResetDuration: 0,
		}, []string{"provider", "model"}),

		UpstreamErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gandalf",
			Name:      "upstream_errors_total",
			Help:      "Total upstream provider errors.",
		}, []string{"provider", "status"}),

		CacheHits: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "gandalf",
			Name:      "cache_hits_total",
			Help:      "Total response cache hits.",
		}),

		CacheMisses: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "gandalf",
			Name:      "cache_misses_total",
			Help:      "Total response cache misses.",
		}),

		RateLimitRejects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gandalf",
			Name:      "ratelimit_rejects_total",
			Help:      "Total rate limit rejections.",
		}, []string{"type"}),

		TokensProcessed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gandalf",
			Name:      "tokens_processed_total",
			Help:      "Total tokens processed.",
		}, []string{"model", "type"}),

		UsageQueueLength: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "gandalf",
			Name:      "usage_queue_length",
			Help:      "Current number of queued usage records.",
		}),
	}

	reg.MustRegister(
		m.RequestsTotal,
		m.RequestDuration,
		m.ActiveRequests,
		m.UpstreamDuration,
		m.UpstreamErrors,
		m.CacheHits,
		m.CacheMisses,
		m.RateLimitRejects,
		m.TokensProcessed,
		m.UsageQueueLength,
	)

	return m
}
