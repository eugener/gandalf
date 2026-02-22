// Package server implements the HTTP transport layer for the Gandalf gateway.
package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
)

// ReadyChecker reports whether the system is ready to serve traffic.
type ReadyChecker func(ctx context.Context) error

// Deps holds all dependencies for the HTTP server.
type Deps struct {
	Auth       gateway.Authenticator
	Proxy      *app.ProxyService
	Keys       *app.KeyManager
	ReadyCheck ReadyChecker // nil = always ready (for tests)
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

	// Client-facing API (auth required)
	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Post("/v1/chat/completions", s.handleChatCompletion)
	})

	return r
}

type server struct {
	deps Deps
}
