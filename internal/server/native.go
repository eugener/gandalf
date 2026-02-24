package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
)

// isValidParam checks that s is non-empty and contains only [a-zA-Z0-9._-].
func isValidParam(s string) bool {
	if len(s) == 0 || len(s) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// mountNativeRoutes registers native API passthrough routes on the given router.
// Each format group uses normalizeAuth to map provider-specific auth headers
// to Authorization: Bearer before the authenticate middleware runs.
func (s *server) mountNativeRoutes(r chi.Router) {
	if s.deps.Providers == nil || s.deps.Router == nil {
		return
	}

	// --- Anthropic native: /v1/messages ---
	r.Group(func(r chi.Router) {
		r.Use(normalizeAuth("X-Api-Key"))
		r.Use(s.authenticate)
		r.Use(s.rateLimit)
		r.Post("/v1/messages", s.handleNativeProxy(
			"anthropic",
			func(_ *http.Request) string { return "/messages" },
			func(_ *http.Request, body []byte) string {
				return gjson.GetBytes(body, "model").String()
			},
		))
	})

	// --- Gemini native: /v1beta/models/* ---
	r.Group(func(r chi.Router) {
		r.Use(normalizeAuth("X-Goog-Api-Key"))
		r.Use(s.authenticate)
		r.Use(s.rateLimit)

		// generateContent, streamGenerateContent, embedContent
		r.Post("/v1beta/models/{model}:{action}", s.handleNativeProxy(
			"gemini",
			func(r *http.Request) string {
				model := chi.URLParam(r, "model")
				action := chi.URLParam(r, "action")
				if !isValidParam(model) || !isValidParam(action) {
					return ""
				}
				return "/models/" + model + ":" + action
			},
			func(r *http.Request, _ []byte) string {
				return chi.URLParam(r, "model")
			},
		))

		// GET /v1beta/models -- list models (no model routing needed)
		r.Get("/v1beta/models", s.handleNativeProxyList("gemini", "/models"))
	})

	// --- Azure OpenAI native: /openai/deployments/{deployment}/* ---
	r.Group(func(r chi.Router) {
		r.Use(normalizeAuth("Api-Key"))
		r.Use(s.authenticate)
		r.Use(s.rateLimit)

		r.Post("/openai/deployments/{deployment}/chat/completions", s.handleNativeProxy(
			"openai",
			func(_ *http.Request) string { return "/chat/completions" },
			func(r *http.Request, _ []byte) string {
				d := chi.URLParam(r, "deployment")
				if !isValidParam(d) {
					return ""
				}
				return d
			},
		))
		r.Post("/openai/deployments/{deployment}/embeddings", s.handleNativeProxy(
			"openai",
			func(_ *http.Request) string { return "/embeddings" },
			func(r *http.Request, _ []byte) string {
				d := chi.URLParam(r, "deployment")
				if !isValidParam(d) {
					return ""
				}
				return d
			},
		))
	})

	// --- Ollama native: /api/* ---
	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Use(s.rateLimit)

		r.Post("/api/chat", s.handleNativeProxy(
			"ollama",
			func(_ *http.Request) string { return "/chat" },
			func(_ *http.Request, body []byte) string {
				return gjson.GetBytes(body, "model").String()
			},
		))
		r.Post("/api/embed", s.handleNativeProxy(
			"ollama",
			func(_ *http.Request) string { return "/embed" },
			func(_ *http.Request, body []byte) string {
				return gjson.GetBytes(body, "model").String()
			},
		))
		r.Get("/api/tags", s.handleNativeProxyList("ollama", "/tags"))
	})
}

// handleNativeProxy returns a handler that authenticates, extracts the model,
// routes to a provider, and forwards the raw request/response.
func (s *server) handleNativeProxy(providerType string,
	pathFunc func(*http.Request) string,
	modelFunc func(*http.Request, []byte) string) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {
		// Read body for model extraction (capped at 4 MB).
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse("failed to read request body"))
			return
		}

		model := modelFunc(r, body)
		if model == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse("model not specified"))
			return
		}

		// Model allowlist check.
		identity := gateway.IdentityFromContext(r.Context())
		if identity != nil && !identity.IsModelAllowed(model) {
			writeJSON(w, http.StatusForbidden, errorResponse("model not allowed"))
			return
		}

		// Route model -> provider targets.
		targets, err := s.deps.Router.ResolveModel(r.Context(), model)
		if err != nil {
			writeUpstreamError(w, r.Context(), err)
			return
		}

		// Find matching provider that implements NativeProxy and has the right type.
		for _, target := range targets {
			p, pErr := s.deps.Providers.Get(target.ProviderID)
			if pErr != nil {
				continue
			}
			if p.Type() != providerType {
				continue
			}
			np, ok := p.(gateway.NativeProxy)
			if !ok {
				continue
			}

			// Reconstruct body and forward.
			r.Body = io.NopCloser(bytes.NewReader(body))
			path := pathFunc(r)
			if path == "" {
				writeJSON(w, http.StatusBadRequest, errorResponse("invalid path parameters"))
				return
			}
			if proxyErr := np.ProxyRequest(r.Context(), w, r, path); proxyErr != nil {
				slog.LogAttrs(r.Context(), slog.LevelError, "native proxy error",
					slog.String("provider", target.ProviderID),
					slog.String("error", proxyErr.Error()),
				)
			}
			return
		}

		writeJSON(w, http.StatusBadGateway, errorResponse("no "+providerType+" provider available for model: "+model))
	}
}

// handleNativeProxyList returns a handler for list endpoints that don't need
// model-based routing (e.g. GET /v1beta/models, GET /api/tags).
func (s *server) handleNativeProxyList(providerType, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Find any registered provider of the given type.
		p, err := s.deps.Providers.GetByType(providerType)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse("no "+providerType+" provider registered"))
			return
		}
		np, ok := p.(gateway.NativeProxy)
		if !ok {
			writeJSON(w, http.StatusBadGateway, errorResponse(providerType+" provider does not support native passthrough"))
			return
		}
		if proxyErr := np.ProxyRequest(r.Context(), w, r, path); proxyErr != nil {
			slog.LogAttrs(r.Context(), slog.LevelError, "native proxy list error",
				slog.String("provider", providerType),
				slog.String("error", proxyErr.Error()),
			)
		}
	}
}

// normalizeAuth returns middleware that copies a provider-specific auth header
// to Authorization: Bearer, so the existing authenticate middleware works
// unchanged. If Authorization is already present, the provider header is ignored.
func normalizeAuth(header string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				if key := r.Header.Get(header); key != "" {
					r.Header.Set("Authorization", "Bearer "+key)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
