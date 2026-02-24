package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/eugener/gandalf/internal/telemetry"
)

// statusText maps HTTP status codes to pre-allocated strings,
// avoiding a strconv.Itoa allocation per request.
var statusText [600]string

func init() {
	for i := range statusText {
		statusText[i] = strconv.Itoa(i)
	}
}

// metricsMiddleware records request duration, status, and active count.
func metricsMiddleware(m *telemetry.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.ActiveRequests.Inc()
			start := time.Now()

			sw := statusWriterPool.Get().(*statusWriter)
			sw.ResponseWriter = w
			sw.status = http.StatusOK
			sw.wroteHeader = false

			next.ServeHTTP(sw, r)

			elapsed := time.Since(start).Seconds()
			status := sw.status
			sw.ResponseWriter = nil
			statusWriterPool.Put(sw)

			m.ActiveRequests.Dec()

			pattern := routePattern(r)
			statusStr := statusText[status]

			m.RequestsTotal.WithLabelValues(r.Method, pattern, statusStr).Inc()
			m.RequestDuration.WithLabelValues(r.Method, pattern).Observe(elapsed)
		})
	}
}

// routePattern returns the chi route pattern for bounded cardinality,
// falling back to the raw path for non-chi routes.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx != nil && rctx.RoutePattern() != "" {
		return rctx.RoutePattern()
	}
	return r.URL.Path
}
