package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/ratelimit"
)

// bodyPool reuses buffers for request body reads, avoiding per-request
// allocations from json.NewDecoder (which cannot be pooled/reset).
var bodyPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// maxRequestBody is the maximum allowed request body size (4 MB).
const maxRequestBody = 4 << 20

// decodeRequestBody reads the request body via bodyPool, unmarshals JSON into
// v, and returns false (writing a 400) on error. Parse errors are logged
// server-side; clients receive a static message to avoid leaking internals.
//
// Uses concrete any parameter instead of generics: Go's generic shape
// dictionary adds +1 alloc/op from interface boxing on every call.
func decodeRequestBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	buf := bodyPool.Get().(*bytes.Buffer)
	buf.Reset()
	if _, err := buf.ReadFrom(r.Body); err != nil {
		bodyPool.Put(buf)
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return false
	}
	if err := json.Unmarshal(buf.Bytes(), v); err != nil {
		bodyPool.Put(buf)
		slog.LogAttrs(r.Context(), slog.LevelWarn, "request decode error",
			slog.String("error", err.Error()),
		)
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return false
	}
	bodyPool.Put(buf)
	return true
}

func (s *server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var req gateway.ChatRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}

	// Model allowlist check.
	identity := gateway.IdentityFromContext(r.Context())
	if identity != nil && !identity.IsModelAllowed(req.Model) {
		writeJSON(w, http.StatusForbidden, errorResponse("model not allowed"))
		return
	}

	// TPM rate limit check (after body decode).
	estimated := int64(100)
	if s.deps.TokenCounter != nil {
		estimated = int64(s.deps.TokenCounter.EstimateRequest(req.Model, req.Messages))
	}

	if !s.consumeTPM(w, identity, estimated) {
		return
	}

	// Cache check (non-streaming only). Guard identity != nil to prevent
	// nil-pointer dereference when auth middleware is bypassed (e.g. tests).
	if !req.Stream && s.deps.Cache != nil && identity != nil && isCacheable(&req) {
		key := cacheKey(identity.KeyID, &req)
		if data, ok := s.deps.Cache.Get(r.Context(), key); ok {
			if s.deps.Metrics != nil {
				s.deps.Metrics.CacheHits.Inc()
			}
			s.recordUsage(r, identity, req.Model, nil, 0, true)
			w.Header()["Content-Type"] = jsonCT
			w.WriteHeader(http.StatusOK)
			w.Write(data)
			return
		}
		if s.deps.Metrics != nil {
			s.deps.Metrics.CacheMisses.Inc()
		}
	}

	if req.Stream {
		s.handleChatCompletionStream(w, r, &req, identity, estimated)
		return
	}

	start := time.Now()
	resp, err := s.deps.Proxy.ChatCompletion(r.Context(), &req)
	elapsed := time.Since(start)
	if err != nil {
		writeUpstreamError(w, r.Context(), err)
		return
	}

	s.adjustTPM(identity, estimated, resp.Usage)

	// Cache store.
	if s.deps.Cache != nil && identity != nil && isCacheable(&req) {
		if data, err := json.Marshal(resp); err == nil {
			s.deps.Cache.Set(r.Context(), cacheKey(identity.KeyID, &req), data, s.cacheTTL(r.Context(), &req))
		}
	}

	s.recordUsage(r, identity, req.Model, resp.Usage, elapsed, false)
	writeJSON(w, http.StatusOK, resp)
}

// handleChatCompletionStream handles SSE streaming chat completion requests.
func (s *server) handleChatCompletionStream(w http.ResponseWriter, r *http.Request, req *gateway.ChatRequest, identity *gateway.Identity, estimated int64) {
	start := time.Now()
	ch, err := s.deps.Proxy.ChatCompletionStream(r.Context(), req)
	if err != nil {
		writeUpstreamError(w, r.Context(), err)
		return
	}

	writeSSEHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("ResponseWriter does not implement http.Flusher")
		return
	}
	flusher.Flush()

	// Lazy ticker: avoid allocating time.NewTicker for fast-completing streams
	// (saves ~3 allocs/op on short responses and benchmarks).
	var keepAlive *time.Ticker
	defer func() {
		if keepAlive != nil {
			keepAlive.Stop()
		}
	}()

	var usage *gateway.Usage
	for {
		// Fast path: drain channel without ticker select when possible.
		if keepAlive == nil {
			select {
			case chunk, chOpen := <-ch:
				if usage, ok = s.processStreamChunk(w, flusher, r, chunk, chOpen, req, identity, estimated, usage, start); !ok {
					return
				}
				// First data chunk sent; start keep-alive for long streams.
				keepAlive = time.NewTicker(15 * time.Second)
			case <-r.Context().Done():
				return
			}
			continue
		}

		select {
		case chunk, chOpen := <-ch:
			if usage, ok = s.processStreamChunk(w, flusher, r, chunk, chOpen, req, identity, estimated, usage, start); !ok {
				return
			}
		case <-keepAlive.C:
			writeSSEKeepAlive(w)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// processStreamChunk handles a single chunk from the stream channel.
// Returns updated usage and true to continue, or false if the stream ended.
// Extracted from inline select branches to DRY the fast-path and keep-alive
// loops without closures (which would add +1 alloc/op).
func (s *server) processStreamChunk(
	w http.ResponseWriter, flusher http.Flusher, r *http.Request,
	chunk gateway.StreamChunk, chOpen bool,
	req *gateway.ChatRequest, identity *gateway.Identity, estimated int64,
	usage *gateway.Usage, start time.Time,
) (*gateway.Usage, bool) {
	if !chOpen {
		writeSSEDone(w)
		flusher.Flush()
		s.finishStream(r, req, identity, estimated, usage, start)
		return usage, false
	}
	if chunk.Err != nil {
		slog.LogAttrs(r.Context(), slog.LevelError, "stream error",
			slog.String("error", chunk.Err.Error()),
		)
		writeSSEError(w, "upstream stream error")
		writeSSEDone(w)
		flusher.Flush()
		s.finishStreamError(r, req, identity, estimated, usage, start)
		return usage, false
	}
	if chunk.Usage != nil {
		usage = chunk.Usage
	}
	if chunk.Done {
		writeSSEDone(w)
		flusher.Flush()
		s.finishStream(r, req, identity, estimated, usage, start)
		return usage, false
	}
	writeSSEData(w, chunk.Data)
	flusher.Flush()
	return usage, true
}

// finishStream adjusts TPM and records usage after stream completion.
func (s *server) finishStream(r *http.Request, req *gateway.ChatRequest, identity *gateway.Identity, estimated int64, usage *gateway.Usage, start time.Time) {
	s.adjustTPM(identity, estimated, usage)
	s.recordUsage(r, identity, req.Model, usage, time.Since(start), false)
}

// finishStreamError adjusts TPM and records usage with 502 status on stream error.
func (s *server) finishStreamError(r *http.Request, req *gateway.ChatRequest, identity *gateway.Identity, estimated int64, usage *gateway.Usage, start time.Time) {
	s.adjustTPM(identity, estimated, usage)
	s.recordUsageWithStatus(r, identity, req.Model, usage, time.Since(start), http.StatusBadGateway)
}

// getLimiter returns the rate limiter for the identity, applying default
// RPM/TPM from config when per-key limits are zero.
func (s *server) getLimiter(id *gateway.Identity) *ratelimit.Limiter {
	if s.deps.RateLimiter == nil || id == nil || id.KeyID == "" {
		return nil
	}
	// Fall back to config-level defaults so keys without explicit limits
	// still get rate-limited when global defaults are configured.
	limits := ratelimit.Limits{RPM: id.RPMLimit, TPM: id.TPMLimit}
	if limits.RPM == 0 {
		limits.RPM = s.deps.DefaultRPM
	}
	if limits.TPM == 0 {
		limits.TPM = s.deps.DefaultTPM
	}
	if limits.RPM == 0 && limits.TPM == 0 {
		return nil
	}
	return s.deps.RateLimiter.GetOrCreate(id.KeyID, limits)
}

// consumeTPM checks the TPM limit, sets headers, and returns false if denied.
func (s *server) consumeTPM(w http.ResponseWriter, identity *gateway.Identity, estimated int64) bool {
	if limiter := s.getLimiter(identity); limiter != nil {
		result := limiter.ConsumeTPM(estimated)
		setTPMHeaders(w, result)
		if !result.Allowed {
			if s.deps.Metrics != nil {
				s.deps.Metrics.RateLimitRejects.WithLabelValues("tpm").Inc()
			}
			writeRateLimitError(w, result)
			return false
		}
	}
	return true
}

// adjustTPM corrects the TPM bucket after receiving actual usage.
func (s *server) adjustTPM(identity *gateway.Identity, estimated int64, usage *gateway.Usage) {
	if usage == nil {
		return
	}
	if limiter := s.getLimiter(identity); limiter != nil {
		limiter.AdjustTPM(estimated - int64(usage.TotalTokens))
	}
}

// recordUsageWithStatus sends a usage record with a custom HTTP status code.
func (s *server) recordUsageWithStatus(r *http.Request, identity *gateway.Identity, model string, usage *gateway.Usage, elapsed time.Duration, status int) {
	if s.deps.Usage == nil {
		return
	}
	rec := gateway.UsageRecord{
		Model:      model,
		LatencyMs:  int(elapsed.Milliseconds()),
		StatusCode: status,
		RequestID:  gateway.RequestIDFromContext(r.Context()),
		CreatedAt:  time.Now(),
	}
	if identity != nil {
		rec.KeyID = identity.KeyID
		rec.UserID = identity.UserID
		rec.TeamID = identity.TeamID
		rec.OrgID = identity.OrgID
	}
	if usage != nil {
		rec.PromptTokens = usage.PromptTokens
		rec.CompletionTokens = usage.CompletionTokens
		rec.TotalTokens = usage.TotalTokens
		if s.deps.Metrics != nil {
			s.deps.Metrics.TokensProcessed.WithLabelValues(model, "prompt").Add(float64(usage.PromptTokens))
			s.deps.Metrics.TokensProcessed.WithLabelValues(model, "completion").Add(float64(usage.CompletionTokens))
		}
	}
	if s.deps.Quota != nil && identity != nil && identity.MaxBudget > 0 && usage != nil {
		cost := estimateCost(model, usage)
		rec.CostUSD = cost
		s.deps.Quota.Consume(identity.KeyID, cost)
	}
	s.deps.Usage.Record(rec)
}

// recordUsage sends a usage record to the async recorder and updates token metrics.
func (s *server) recordUsage(r *http.Request, identity *gateway.Identity, model string, usage *gateway.Usage, elapsed time.Duration, cached bool) {
	if s.deps.Usage == nil {
		return
	}
	rec := gateway.UsageRecord{
		Model:      model,
		LatencyMs:  int(elapsed.Milliseconds()),
		StatusCode: http.StatusOK,
		RequestID:  gateway.RequestIDFromContext(r.Context()),
		CreatedAt:  time.Now(),
		Cached:     cached,
	}
	if identity != nil {
		rec.KeyID = identity.KeyID
		rec.UserID = identity.UserID
		rec.TeamID = identity.TeamID
		rec.OrgID = identity.OrgID
	}
	if usage != nil {
		rec.PromptTokens = usage.PromptTokens
		rec.CompletionTokens = usage.CompletionTokens
		rec.TotalTokens = usage.TotalTokens
		// Wire TokensProcessed Prometheus counter so grafana dashboards
		// can track prompt vs completion token volume per model.
		if s.deps.Metrics != nil {
			s.deps.Metrics.TokensProcessed.WithLabelValues(model, "prompt").Add(float64(usage.PromptTokens))
			s.deps.Metrics.TokensProcessed.WithLabelValues(model, "completion").Add(float64(usage.CompletionTokens))
		}
	}
	// Quota consumption.
	if s.deps.Quota != nil && identity != nil && identity.MaxBudget > 0 && usage != nil {
		cost := estimateCost(model, usage)
		rec.CostUSD = cost
		s.deps.Quota.Consume(identity.KeyID, cost)
	}
	s.deps.Usage.Record(rec)
}

// cacheTTL returns the cache TTL for a request. Checks route-level
// cache_ttl_s first (allows per-model TTL tuning), falls back to 5m default.
func (s *server) cacheTTL(ctx context.Context, req *gateway.ChatRequest) time.Duration {
	if s.deps.Router != nil {
		if ttl := s.deps.Router.CacheTTL(ctx, req.Model); ttl > 0 {
			return ttl
		}
	}
	return 5 * time.Minute
}

// estimateCost provides a rough USD cost estimate based on model and token counts.
// These are approximate and should be replaced with a proper pricing table.
func estimateCost(model string, usage *gateway.Usage) float64 {
	if usage == nil {
		return 0
	}
	// Default: $0.01 per 1K tokens (rough average).
	return float64(usage.TotalTokens) * 0.00001
}

type apiError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func errorResponse(msg string) apiError {
	var e apiError
	e.Error.Message = msg
	e.Error.Type = "invalid_request_error"
	return e
}

// writeUpstreamError logs the full error server-side and returns a sanitized
// message to the client. Both 4xx and 5xx responses use generic status text
// to avoid leaking upstream provider internals (URLs, org IDs, quota details).
func writeUpstreamError(w http.ResponseWriter, ctx context.Context, err error) {
	status := errorStatus(err)
	slog.LogAttrs(ctx, slog.LevelError, "upstream error",
		slog.Int("status", status),
		slog.String("error", err.Error()),
	)
	writeJSON(w, status, errorResponse(http.StatusText(status)))
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, gateway.ErrUnauthorized), errors.Is(err, gateway.ErrKeyExpired):
		return http.StatusUnauthorized
	case errors.Is(err, gateway.ErrForbidden), errors.Is(err, gateway.ErrModelNotAllowed), errors.Is(err, gateway.ErrKeyBlocked):
		return http.StatusForbidden
	case errors.Is(err, gateway.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, gateway.ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, gateway.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, gateway.ErrBadRequest):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// jsonCT is a pre-allocated header value slice. Direct map assignment
// (w.Header()["Content-Type"] = jsonCT) avoids the []string{v} alloc
// that Header.Set creates on every call. Saves 1 alloc/req.
var jsonCT = []string{"application/json"}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to encode response", "error", err)
		return
	}
	w.Header()["Content-Type"] = jsonCT
	w.WriteHeader(status)
	w.Write(data)
}
