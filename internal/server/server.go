// Package server implements the HTTP transport layer for the Gandalf gateway.
package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/ratelimit"
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
	ReadyCheck   ReadyChecker         // nil = always ready (for tests)
	Usage        UsageRecorder        // nil = no usage recording
	RateLimiter  *ratelimit.Registry  // nil = no rate limiting
	TokenCounter TokenCounter         // nil = fixed estimate
	Cache        Cache                // nil = no caching
	Quota        QuotaChecker         // nil = no quota enforcement
}

// New creates an http.Handler with all routes and middleware wired.
func New(deps Deps) http.Handler {
	s := &server{deps: deps}

	r := chi.NewRouter()

	// Global middleware
	r.Use(s.recovery)
	r.Use(s.requestID)
	r.Use(s.logging)

	// System endpoints (no auth)
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

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

	return r
}

type server struct {
	deps Deps
}
