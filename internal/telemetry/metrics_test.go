package telemetry

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewMetrics(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewPedanticRegistry()
	m := NewMetrics(reg)

	if m.RequestsTotal == nil {
		t.Error("RequestsTotal is nil")
	}
	if m.RequestDuration == nil {
		t.Error("RequestDuration is nil")
	}
	if m.ActiveRequests == nil {
		t.Error("ActiveRequests is nil")
	}
	if m.UpstreamDuration == nil {
		t.Error("UpstreamDuration is nil")
	}
	if m.UpstreamErrors == nil {
		t.Error("UpstreamErrors is nil")
	}
	if m.CacheHits == nil {
		t.Error("CacheHits is nil")
	}
	if m.CacheMisses == nil {
		t.Error("CacheMisses is nil")
	}
	if m.RateLimitRejects == nil {
		t.Error("RateLimitRejects is nil")
	}
	if m.TokensProcessed == nil {
		t.Error("TokensProcessed is nil")
	}
	if m.UsageQueueLength == nil {
		t.Error("UsageQueueLength is nil")
	}

	// Verify metrics can be gathered without error.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) == 0 {
		t.Error("expected at least one metric family")
	}
}

func TestNewMetricsIncrement(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewPedanticRegistry()
	m := NewMetrics(reg)

	// Increment counters and observe histograms to verify they work.
	m.RequestsTotal.WithLabelValues("POST", "/v1/chat/completions", "200").Inc()
	m.CacheHits.Inc()
	m.CacheMisses.Inc()
	m.ActiveRequests.Set(5)
	m.RequestDuration.WithLabelValues("POST", "/v1/chat/completions").Observe(0.123)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather after increment: %v", err)
	}

	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}

	want := []string{
		"gandalf_requests_total",
		"gandalf_cache_hits_total",
		"gandalf_cache_misses_total",
		"gandalf_active_requests",
		"gandalf_request_duration_seconds",
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("missing metric %q in gathered families", name)
		}
	}
}

// SetupTracing is not unit-tested because it requires a gRPC connection
// to an OTLP collector, which is integration-test territory.
