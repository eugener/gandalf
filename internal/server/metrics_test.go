package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/telemetry"
)

func TestMetricsEndpoint(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := telemetry.NewMetrics(reg)

	provReg := provider.NewRegistry()
	provReg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})

	h := New(Deps{
		Auth:           fakeAuth{},
		Proxy:          app.NewProxyService(provReg, routerSvc),
		Providers:      provReg,
		Router:         routerSvc,
		Metrics:        metrics,
		MetricsHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	})

	// Hit a normal endpoint first to generate metrics.
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Now check /metrics.
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: status = %d; body = %s", rec.Code, rec.Body.String())
	}
	metricsBody := rec.Body.String()
	if !strings.Contains(metricsBody, "gandalf_requests_total") {
		t.Error("metrics should contain gandalf_requests_total")
	}
	if !strings.Contains(metricsBody, "gandalf_request_duration_seconds") {
		t.Error("metrics should contain gandalf_request_duration_seconds")
	}
}

func TestMetricsMiddleware_IncrementsCounters(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := telemetry.NewMetrics(reg)

	provReg := provider.NewRegistry()
	provReg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})

	h := New(Deps{
		Auth:           fakeAuth{},
		Proxy:          app.NewProxyService(provReg, routerSvc),
		Providers:      provReg,
		Router:         routerSvc,
		Metrics:        metrics,
		MetricsHandler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
	})

	// Make a few requests.
	for range 3 {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}

	// Gather metrics and check.
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range families {
		if f.GetName() == "gandalf_requests_total" {
			found = true
			// Should have metrics for healthz.
			for _, m := range f.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "path" && l.GetValue() == "/healthz" {
						if m.GetCounter().GetValue() < 3 {
							t.Errorf("requests_total for /healthz = %f, want >= 3", m.GetCounter().GetValue())
						}
					}
				}
			}
		}
	}
	if !found {
		t.Error("gandalf_requests_total metric not found")
	}
}
