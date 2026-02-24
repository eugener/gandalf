package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/ratelimit"
)

// maxRequestBody is the maximum allowed request body size (4 MB).
const maxRequestBody = 4 << 20

func (s *server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req gateway.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body: "+err.Error()))
		return
	}

	// TPM rate limit check (after body decode).
	identity := gateway.IdentityFromContext(r.Context())
	estimated := int64(100)
	if s.deps.TokenCounter != nil {
		estimated = int64(s.deps.TokenCounter.EstimateRequest(req.Model, req.Messages))
	}

	if !s.consumeTPM(w, identity, estimated) {
		return
	}

	// Cache check (non-streaming only).
	if !req.Stream && s.deps.Cache != nil && isCacheable(&req) {
		key := cacheKey(identity.KeyID, &req)
		if data, ok := s.deps.Cache.Get(r.Context(), key); ok {
			if s.deps.Metrics != nil {
				s.deps.Metrics.CacheHits.Inc()
			}
			s.recordUsage(r, req.Model, nil, 0, true)
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
	if s.deps.Cache != nil && isCacheable(&req) {
		if data, err := json.Marshal(resp); err == nil {
			s.deps.Cache.Set(r.Context(), cacheKey(identity.KeyID, &req), data, s.cacheTTL(&req))
		}
	}

	s.recordUsage(r, req.Model, resp.Usage, elapsed, false)
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

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	var usage *gateway.Usage
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				writeSSEDone(w)
				flusher.Flush()
				s.finishStream(r, req, identity, estimated, usage, start)
				return
			}
			if chunk.Err != nil {
				slog.LogAttrs(r.Context(), slog.LevelError, "stream error",
					slog.String("error", chunk.Err.Error()),
				)
				writeSSEDone(w)
				flusher.Flush()
				s.finishStream(r, req, identity, estimated, usage, start)
				return
			}
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if chunk.Done {
				writeSSEDone(w)
				flusher.Flush()
				s.finishStream(r, req, identity, estimated, usage, start)
				return
			}
			writeSSEData(w, chunk.Data)
			flusher.Flush()

		case <-keepAlive.C:
			writeSSEKeepAlive(w)
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

// finishStream adjusts TPM and records usage after stream completion.
func (s *server) finishStream(r *http.Request, req *gateway.ChatRequest, identity *gateway.Identity, estimated int64, usage *gateway.Usage, start time.Time) {
	s.adjustTPM(identity, estimated, usage)
	s.recordUsage(r, req.Model, usage, time.Since(start), false)
}

// getLimiter returns the rate limiter for the identity, or nil if not configured.
func (s *server) getLimiter(id *gateway.Identity) *ratelimit.Limiter {
	if s.deps.RateLimiter == nil || id == nil || id.KeyID == "" {
		return nil
	}
	limits := ratelimit.Limits{RPM: id.RPMLimit, TPM: id.TPMLimit}
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

// recordUsage sends a usage record to the async recorder.
func (s *server) recordUsage(r *http.Request, model string, usage *gateway.Usage, elapsed time.Duration, cached bool) {
	if s.deps.Usage == nil {
		return
	}
	identity := gateway.IdentityFromContext(r.Context())
	rec := gateway.UsageRecord{
		ID:         uuid.Must(uuid.NewV7()).String(),
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
	}
	// Quota consumption.
	if s.deps.Quota != nil && identity != nil && identity.MaxBudget > 0 && usage != nil {
		cost := estimateCost(model, usage)
		rec.CostUSD = cost
		s.deps.Quota.Consume(identity.KeyID, cost)
	}
	s.deps.Usage.Record(rec)
}

// cacheTTL returns the cache TTL for a request, checking route config or falling back to default.
func (s *server) cacheTTL(req *gateway.ChatRequest) time.Duration {
	// Default 5 minutes; route-level TTL would be resolved here in the future.
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
// message to the client. For server errors (5xx), a generic message is returned
// to avoid leaking upstream provider internals (URLs, org IDs, etc.).
func writeUpstreamError(w http.ResponseWriter, ctx context.Context, err error) {
	status := errorStatus(err)
	if status >= http.StatusInternalServerError {
		slog.LogAttrs(ctx, slog.LevelError, "upstream error", slog.String("error", err.Error()))
		writeJSON(w, status, errorResponse("upstream provider error"))
		return
	}
	writeJSON(w, status, errorResponse(err.Error()))
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
	w.Header()["Content-Type"] = jsonCT
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}
