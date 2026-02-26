package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/ratelimit"
)

// Pre-allocated header key strings in canonical MIME form.
const (
	hdrRateLimitRequests    = "X-Ratelimit-Limit-Requests"
	hdrRemainingRequests    = "X-Ratelimit-Remaining-Requests"
	hdrRateLimitTokens      = "X-Ratelimit-Limit-Tokens"
	hdrRemainingTokens      = "X-Ratelimit-Remaining-Tokens"
	hdrRetryAfter           = "Retry-After"
	maxRequestIDLen         = 128
)

// Pre-allocated header value slices for security headers.
// Direct map assignment avoids the []string{v} alloc that Header.Set creates.
var (
	nosniffVal = []string{"nosniff"}
	denyVal    = []string{"DENY"}
)

// statusWriterPool eliminates 1 alloc/req from &statusWriter{} escaping to heap.
// Reset fields on Get, nil ResponseWriter on Put to avoid retaining references.
var statusWriterPool = sync.Pool{
	New: func() any { return &statusWriter{status: http.StatusOK} },
}

// securityHeaders sets defense-in-depth response headers on every request.
func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h["X-Content-Type-Options"] = nosniffVal
		h["X-Frame-Options"] = denyVal
		next.ServeHTTP(w, r)
	})
}

// recovery catches panics and returns 500.
func (s *server) recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// LogAttrs with typed attrs keeps values on the stack (~2 fewer
				// allocs vs slog.Error which boxes every key+value into any).
				slog.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.Any("error", rec),
					slog.String("path", r.URL.Path),
				)
				writeJSON(w, http.StatusInternalServerError, errorResponse("internal server error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestIDHeader uses the canonical MIME form so direct map access
// (r.Header[key], w.Header()[key] = ...) skips textproto.CanonicalMIMEHeaderKey,
// saving 2 allocs/req that Header.Get/Set would otherwise spend on canonicalization.
const requestIDHeader = "X-Request-Id"

// requestID adds a UUID v7 request ID to the context and response header.
// Client-provided IDs are validated: max 128 chars, [a-zA-Z0-9._-] only.
// Invalid or missing IDs are replaced with a fresh UUID v7.
func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id string
		if vals := r.Header[requestIDHeader]; len(vals) > 0 && isValidRequestID(vals[0]) {
			id = vals[0]
		} else {
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header()[requestIDHeader] = []string{id}
		ctx := gateway.ContextWithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isValidToken checks that s is non-empty, at most maxLen chars, and contains
// only [a-zA-Z0-9._-]. Shared by isValidRequestID and isValidParam to DRY
// the identical byte-loop validation that was duplicated in both.
func isValidToken(s string, maxLen int) bool {
	if len(s) == 0 || len(s) > maxLen {
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

// isValidRequestID checks that s is a valid request ID (max 128 chars, [a-zA-Z0-9._-]).
func isValidRequestID(s string) bool { return isValidToken(s, maxRequestIDLen) }

// logging logs each request with method, path, status, and duration.
func (s *server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := statusWriterPool.Get().(*statusWriter)
		sw.ResponseWriter = w
		sw.status = http.StatusOK
		sw.wroteHeader = false
		next.ServeHTTP(sw, r)
		// LogAttrs with typed slog.String/Int/Int64 keeps attrs as stack values,
		// saving ~5 allocs/req vs slog.Info which boxes every key+value into any.
		slog.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("request_id", gateway.RequestIDFromContext(r.Context())),
		)
		sw.ResponseWriter = nil
		statusWriterPool.Put(sw)
	})
}

// authenticate validates credentials and injects Identity into context.
// When requestMeta already exists in context (set by requestID middleware),
// the identity is stored by mutation -- no new context or request copy needed.
func (s *server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := s.deps.Auth.Authenticate(r.Context(), r)
		if err != nil {
			status := errorStatus(err)
			writeJSON(w, status, errorResponse(err.Error()))
			return
		}
		ctx := gateway.ContextWithIdentity(r.Context(), identity)
		if ctx == r.Context() {
			// Identity was stored via pointer mutation; skip Request.WithContext.
			next.ServeHTTP(w, r)
		} else {
			next.ServeHTTP(w, r.WithContext(ctx))
		}
	})
}

// statusWriter wraps ResponseWriter to capture the HTTP status code.
// WriteHeader records only the first status code; subsequent calls are
// forwarded to the underlying writer but do not update the captured value,
// matching net/http semantics where only the first WriteHeader takes effect.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.wroteHeader {
		sw.status = code
		sw.wroteHeader = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wroteHeader {
		sw.wroteHeader = true
	}
	return sw.ResponseWriter.Write(b)
}

// Flush delegates to the underlying ResponseWriter if it implements http.Flusher.
// This ensures SSE streaming works through middleware.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter, allowing http.ResponseController
// and similar utilities to find interface implementations.
func (sw *statusWriter) Unwrap() http.ResponseWriter {
	return sw.ResponseWriter
}

// rateLimit enforces per-key RPM rate limiting and quota checks.
// TPM limiting is handled in the handlers after body decode.
func (s *server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := gateway.IdentityFromContext(r.Context())
		if identity == nil || identity.KeyID == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Quota check.
		if s.deps.Quota != nil && identity.MaxBudget > 0 {
			if !s.deps.Quota.Check(identity.KeyID, identity.MaxBudget) {
				writeJSON(w, http.StatusTooManyRequests, errorResponse("quota exceeded"))
				return
			}
		}

		// RPM check.
		if s.deps.RateLimiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		// Fall back to config-level defaults so keys without explicit limits
		// still get rate-limited when global defaults are configured.
		limits := ratelimit.Limits{RPM: identity.RPMLimit, TPM: identity.TPMLimit}
		if limits.RPM == 0 {
			limits.RPM = s.deps.DefaultRPM
		}
		if limits.TPM == 0 {
			limits.TPM = s.deps.DefaultTPM
		}
		if limits.RPM == 0 && limits.TPM == 0 {
			next.ServeHTTP(w, r)
			return
		}

		limiter := s.deps.RateLimiter.GetOrCreate(identity.KeyID, limits)
		result := limiter.AllowRPM()
		setRPMHeaders(w, result)

		if !result.Allowed {
			if s.deps.Metrics != nil {
				s.deps.Metrics.RateLimitRejects.WithLabelValues("rpm").Inc()
			}
			writeRateLimitError(w, result)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// setRPMHeaders sets RPM rate limit headers on the response.
func setRPMHeaders(w http.ResponseWriter, r ratelimit.Result) {
	if r.Limit == 0 {
		return
	}
	h := w.Header()
	h[hdrRateLimitRequests] = []string{strconv.FormatInt(r.Limit, 10)}
	h[hdrRemainingRequests] = []string{strconv.FormatInt(r.Remaining, 10)}
}

// setTPMHeaders sets TPM rate limit headers on the response.
func setTPMHeaders(w http.ResponseWriter, r ratelimit.Result) {
	if r.Limit == 0 {
		return
	}
	h := w.Header()
	h[hdrRateLimitTokens] = []string{strconv.FormatInt(r.Limit, 10)}
	h[hdrRemainingTokens] = []string{strconv.FormatInt(r.Remaining, 10)}
}

// tracingMiddleware creates a span for each HTTP request.
func tracingMiddleware(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, span := tracer.Start(r.Context(), r.Method+" "+r.URL.Path,
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.url", r.URL.Path),
					attribute.String("http.request_id", gateway.RequestIDFromContext(r.Context())),
				),
			)
			defer span.End()

			sw := statusWriterPool.Get().(*statusWriter)
			sw.ResponseWriter = w
			sw.status = http.StatusOK
			sw.wroteHeader = false

			next.ServeHTTP(sw, r.WithContext(ctx))

			span.SetAttributes(attribute.Int("http.status_code", sw.status))
			sw.ResponseWriter = nil
			statusWriterPool.Put(sw)
		})
	}
}

// requirePerm returns middleware that checks the caller's identity for the given permission.
func (s *server) requirePerm(perm gateway.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := gateway.IdentityFromContext(r.Context())
			if identity == nil {
				writeJSON(w, http.StatusUnauthorized, errorResponse("unauthorized"))
				return
			}
			if !identity.Can(perm) {
				writeJSON(w, http.StatusForbidden, errorResponse("insufficient permissions"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeRateLimitError writes a 429 response with Retry-After header.
func writeRateLimitError(w http.ResponseWriter, r ratelimit.Result) {
	if r.RetryAfterSeconds > 0 {
		w.Header()[hdrRetryAfter] = []string{strconv.Itoa(int(r.RetryAfterSeconds) + 1)}
	}
	writeJSON(w, http.StatusTooManyRequests, errorResponse("rate limit exceeded"))
}
