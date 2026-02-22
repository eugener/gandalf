package server

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	gateway "github.com/eugener/gandalf/internal"
)

// statusWriterPool eliminates 1 alloc/req from &statusWriter{} escaping to heap.
// Reset fields on Get, nil ResponseWriter on Put to avoid retaining references.
var statusWriterPool = sync.Pool{
	New: func() any { return &statusWriter{} },
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
func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id string
		if vals := r.Header[requestIDHeader]; len(vals) > 0 {
			id = vals[0]
		} else {
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header()[requestIDHeader] = []string{id}
		ctx := gateway.ContextWithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

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
