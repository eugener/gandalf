// Package server implements the HTTP transport layer for the Gandalf gateway.
package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"go.opentelemetry.io/otel/trace"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/ratelimit"
	"github.com/eugener/gandalf/internal/storage"
	"github.com/eugener/gandalf/internal/telemetry"
)

// ReadyChecker reports whether the system is ready to serve traffic.
type ReadyChecker func(ctx context.Context) error

// UsageRecorder records API usage asynchronously.
type UsageRecorder interface {
	Record(gateway.UsageRecord)
}

// TokenCounter estimates token counts for request messages.
type TokenCounter interface {
	EstimateRequest(model string, messages []gateway.Message) int
}

// QuotaChecker verifies and tracks spend budgets.
type QuotaChecker interface {
	Check(keyID string, limit float64) bool
	Consume(keyID string, costUSD float64)
}

// Deps holds all dependencies for the HTTP server.
type Deps struct {
	Auth         gateway.Authenticator
	Proxy        *app.ProxyService
	Providers    *provider.Registry   // needed for NativeProxy type assertion
	Router       *app.RouterService   // needed for model -> provider routing
	Keys         *app.KeyManager
	Store          storage.Store        // nil = no admin CRUD (for tests)
	Metrics        *telemetry.Metrics  // nil = no Prometheus metrics
	MetricsHandler http.Handler        // nil = no /metrics endpoint
	Tracer         trace.Tracer        // nil = no distributed tracing
	ReadyCheck     ReadyChecker        // nil = always ready (for tests)
	Usage        UsageRecorder        // nil = no usage recording
	RateLimiter  *ratelimit.Registry  // nil = no rate limiting
	TokenCounter TokenCounter         // nil = fixed estimate
	Cache        Cache                // nil = no caching
	Quota        QuotaChecker         // nil = no quota enforcement
	DefaultRPM   int64               // fallback RPM when per-key is 0
	DefaultTPM   int64               // fallback TPM when per-key is 0
}

// New creates an http.Handler with all routes and middleware wired.
func New(deps Deps) http.Handler {
	s := &server{deps: deps}

	r := chi.NewRouter()

	// Global middleware
	r.Use(s.securityHeaders)
	r.Use(s.recovery)
	r.Use(s.requestID)
	r.Use(s.logging)
	if deps.Metrics != nil {
		r.Use(metricsMiddleware(deps.Metrics))
	}
	if deps.Tracer != nil {
		r.Use(tracingMiddleware(deps.Tracer))
	}

	// System endpoints (no auth)
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	if deps.MetricsHandler != nil {
		r.Handle("/metrics", deps.MetricsHandler)
	}

	// Client-facing API (auth required) -- universal OpenAI-format
	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Use(s.rateLimit)
		r.Post("/v1/chat/completions", s.handleChatCompletion)
		r.Post("/v1/embeddings", s.handleEmbeddings)
		r.Get("/v1/models", s.handleListModels)
	})

	// Native API passthrough routes (per-provider auth normalization)
	s.mountNativeRoutes(r)

	// Admin API (auth + RBAC required)
	if deps.Store != nil {
		r.Route("/admin/v1", func(r chi.Router) {
			r.Use(s.authenticate)

			r.Group(func(r chi.Router) {
				r.Use(s.requirePerm(gateway.PermManageProviders))
				r.Get("/providers", s.handleListProviders)
				r.Post("/providers", s.handleCreateProvider)
				r.Get("/providers/{id}", s.handleGetProvider)
				r.Put("/providers/{id}", s.handleUpdateProvider)
				r.Delete("/providers/{id}", s.handleDeleteProvider)
				r.Post("/cache/purge", s.handleCachePurge)
			})

			r.Group(func(r chi.Router) {
				r.Use(s.requirePerm(gateway.PermManageAllKeys))
				r.Get("/keys", s.handleListKeys)
				r.Post("/keys", s.handleCreateKey)
				r.Get("/keys/{id}", s.handleGetKey)
				r.Put("/keys/{id}", s.handleUpdateKey)
				r.Delete("/keys/{id}", s.handleDeleteKey)
			})

			r.Group(func(r chi.Router) {
				r.Use(s.requirePerm(gateway.PermManageRoutes))
				r.Get("/routes", s.handleListRoutes)
				r.Post("/routes", s.handleCreateRoute)
				r.Get("/routes/{id}", s.handleGetRoute)
				r.Put("/routes/{id}", s.handleUpdateRoute)
				r.Delete("/routes/{id}", s.handleDeleteRoute)
			})

			r.Group(func(r chi.Router) {
				r.Use(s.requirePerm(gateway.PermViewAllUsage))
				r.Get("/usage", s.handleQueryUsage)
				r.Get("/usage/summary", s.handleUsageSummary)
			})
		})
	}

	return r
}

type server struct {
	deps Deps
}
