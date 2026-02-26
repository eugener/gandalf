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
	CacheHits        prometheus.Counter
	CacheMisses      prometheus.Counter
	RateLimitRejects *prometheus.CounterVec
	TokensProcessed       *prometheus.CounterVec
	CircuitBreakerState   *prometheus.GaugeVec   // labels: provider, state
	CircuitBreakerRejects *prometheus.CounterVec  // labels: provider
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

		CircuitBreakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "gandalf",
			Name:      "circuit_breaker_state",
			Help:      "Circuit breaker state per provider (0=closed, 1=open, 2=half_open).",
		}, []string{"provider"}),

		CircuitBreakerRejects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "gandalf",
			Name:      "circuit_breaker_rejects_total",
			Help:      "Total requests rejected by circuit breaker.",
		}, []string{"provider"}),
	}

	reg.MustRegister(
		m.RequestsTotal,
		m.RequestDuration,
		m.ActiveRequests,
		m.CacheHits,
		m.CacheMisses,
		m.RateLimitRejects,
		m.TokensProcessed,
		m.CircuitBreakerState,
		m.CircuitBreakerRejects,
	)

	return m
}
