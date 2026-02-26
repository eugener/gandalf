package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/circuitbreaker"
	"github.com/eugener/gandalf/internal/provider"
)

// ProxyService forwards chat completion requests to the appropriate LLM provider
// based on model routing configuration. It supports priority failover: on
// provider/network errors it tries the next target; on client errors (4xx)
// it returns immediately.
type ProxyService struct {
	providers *provider.Registry
	router    *RouterService
	tracer    trace.Tracer                // nil disables tracing (saves ~3.7 allocs/op)
	breakers  *circuitbreaker.Registry    // nil disables circuit breaking
}

// NewProxyService returns a ProxyService wired to the given provider registry and router.
// Pass a nil tracer to disable tracing (avoids span/attribute allocations on hot paths).
// Pass a nil breakers registry to disable circuit breaking.
func NewProxyService(providers *provider.Registry, router *RouterService, tracer trace.Tracer, breakers *circuitbreaker.Registry) *ProxyService {
	return &ProxyService{providers: providers, router: router, tracer: tracer, breakers: breakers}
}

// ChatCompletion resolves the requested model to providers via routing rules
// and forwards the chat completion request with priority failover.
//
// The failover loop is inlined (not a generic helper) because Go's generic
// shape dictionary + closure costs +1 alloc/op on this hot path.
func (ps *ProxyService) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	targets, err := ps.router.ResolveModel(ctx, req.Model)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, target := range targets {
		if ps.breakers != nil {
			if cb := ps.breakers.Get(target.ProviderID); cb != nil && !cb.Allow() {
				lastErr = fmt.Errorf("%w: circuit breaker open for %s", gateway.ErrProviderError, target.ProviderID)
				continue
			}
		}

		p, err := ps.providers.Get(target.ProviderID)
		if err != nil {
			// Use %w (not %v) to preserve error chain for errors.Is upstream.
			lastErr = fmt.Errorf("%w: %w", gateway.ErrProviderError, err)
			continue
		}

		origModel := req.Model
		req.Model = target.Model

		callCtx := ctx
		var span trace.Span
		if ps.tracer != nil {
			callCtx, span = ps.tracer.Start(ctx, "provider.ChatCompletion",
				trace.WithAttributes(
					attribute.String("provider", target.ProviderID),
					attribute.String("model", target.Model),
				),
			)
		}
		resp, err := p.ChatCompletion(callCtx, req)
		if span != nil {
			span.End()
		}
		req.Model = origModel

		if err != nil {
			ps.recordBreakerError(target.ProviderID, err)
			if lastErr, ok := failoverErr(ctx, err, target.ProviderID, "provider failed, trying next"); ok {
				return nil, lastErr
			}
			lastErr = fmt.Errorf("%w: %w", gateway.ErrProviderError, err)
			continue
		}
		ps.recordBreakerSuccess(target.ProviderID)
		return resp, nil
	}
	return nil, lastErr
}

// ChatCompletionStream resolves the model and forwards a streaming request
// with priority failover.
func (ps *ProxyService) ChatCompletionStream(ctx context.Context, req *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	targets, err := ps.router.ResolveModel(ctx, req.Model)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, target := range targets {
		if ps.breakers != nil {
			if cb := ps.breakers.Get(target.ProviderID); cb != nil && !cb.Allow() {
				lastErr = fmt.Errorf("%w: circuit breaker open for %s", gateway.ErrProviderError, target.ProviderID)
				continue
			}
		}

		p, err := ps.providers.Get(target.ProviderID)
		if err != nil {
			lastErr = fmt.Errorf("%w: %w", gateway.ErrProviderError, err)
			continue
		}

		origModel := req.Model
		req.Model = target.Model
		ch, err := p.ChatCompletionStream(ctx, req)
		req.Model = origModel

		if err != nil {
			ps.recordBreakerError(target.ProviderID, err)
			if lastErr, ok := failoverErr(ctx, err, target.ProviderID, "provider stream failed, trying next"); ok {
				return nil, lastErr
			}
			lastErr = fmt.Errorf("%w: %w", gateway.ErrProviderError, err)
			continue
		}
		ps.recordBreakerSuccess(target.ProviderID)
		return ch, nil
	}
	return nil, lastErr
}

// Embeddings resolves the model and forwards an embedding request with
// priority failover.
func (ps *ProxyService) Embeddings(ctx context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	targets, err := ps.router.ResolveModel(ctx, req.Model)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, target := range targets {
		if ps.breakers != nil {
			if cb := ps.breakers.Get(target.ProviderID); cb != nil && !cb.Allow() {
				lastErr = fmt.Errorf("%w: circuit breaker open for %s", gateway.ErrProviderError, target.ProviderID)
				continue
			}
		}

		p, err := ps.providers.Get(target.ProviderID)
		if err != nil {
			lastErr = fmt.Errorf("%w: %w", gateway.ErrProviderError, err)
			continue
		}

		origModel := req.Model
		req.Model = target.Model
		resp, err := p.Embeddings(ctx, req)
		req.Model = origModel

		if err != nil {
			ps.recordBreakerError(target.ProviderID, err)
			if lastErr, ok := failoverErr(ctx, err, target.ProviderID, "provider embeddings failed, trying next"); ok {
				return nil, lastErr
			}
			lastErr = fmt.Errorf("%w: %w", gateway.ErrProviderError, err)
			continue
		}
		ps.recordBreakerSuccess(target.ProviderID)
		return resp, nil
	}
	return nil, lastErr
}

// failoverErr checks whether err is a client error (non-retriable). If so it
// returns (err, true). Otherwise it logs a warning and returns ("", false) so
// the caller continues to the next target. Kept as a helper to avoid repeating
// the log+check pattern in every failover loop.
func failoverErr(ctx context.Context, err error, providerID, msg string) (error, bool) {
	if isClientError(err) {
		return err, true
	}
	slog.LogAttrs(ctx, slog.LevelWarn, msg,
		slog.String("provider", providerID),
		slog.String("error", err.Error()),
	)
	return nil, false
}

// ListModels aggregates model lists from all registered providers.
func (ps *ProxyService) ListModels(ctx context.Context) ([]string, error) {
	var all []string
	for _, name := range ps.providers.List() {
		p, err := ps.providers.Get(name)
		if err != nil {
			continue
		}
		models, err := p.ListModels(ctx)
		if err != nil {
			continue
		}
		all = append(all, models...)
	}
	return all, nil
}

// recordBreakerSuccess records a successful request to the circuit breaker.
func (ps *ProxyService) recordBreakerSuccess(providerID string) {
	if ps.breakers != nil {
		ps.breakers.GetOrCreate(providerID).RecordSuccess()
	}
}

// recordBreakerError records a failed request to the circuit breaker.
func (ps *ProxyService) recordBreakerError(providerID string, err error) {
	if ps.breakers != nil {
		weight := circuitbreaker.ClassifyError(err)
		if weight > 0 {
			ps.breakers.GetOrCreate(providerID).RecordError(weight)
		}
	}
}

// httpStatusError is an interface for errors that carry an HTTP status code.
type httpStatusError interface {
	HTTPStatus() int
}

// isClientError returns true if the error represents a client-side error
// (4xx) that should not trigger failover.
func isClientError(err error) bool {
	// Check if the error carries an HTTP status code.
	var he httpStatusError
	if errors.As(err, &he) {
		code := he.HTTPStatus()
		return code >= http.StatusBadRequest && code < http.StatusInternalServerError
	}
	// Treat domain-level client errors as non-retriable.
	return errors.Is(err, gateway.ErrBadRequest) ||
		errors.Is(err, gateway.ErrUnauthorized) ||
		errors.Is(err, gateway.ErrForbidden) ||
		errors.Is(err, gateway.ErrModelNotAllowed) ||
		errors.Is(err, gateway.ErrKeyExpired) ||
		errors.Is(err, gateway.ErrKeyBlocked)
}
