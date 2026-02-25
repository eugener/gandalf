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
	"github.com/eugener/gandalf/internal/provider"
)

// ProxyService forwards chat completion requests to the appropriate LLM provider
// based on model routing configuration. It supports priority failover: on
// provider/network errors it tries the next target; on client errors (4xx)
// it returns immediately.
type ProxyService struct {
	providers *provider.Registry
	router    *RouterService
	tracer    trace.Tracer // nil disables tracing (saves ~3.7 allocs/op)
}

// NewProxyService returns a ProxyService wired to the given provider registry and router.
// Pass a nil tracer to disable tracing (avoids span/attribute allocations on hot paths).
func NewProxyService(providers *provider.Registry, router *RouterService, tracer trace.Tracer) *ProxyService {
	return &ProxyService{providers: providers, router: router, tracer: tracer}
}

// ChatCompletion resolves the requested model to providers via routing rules
// and forwards the chat completion request with priority failover.
func (ps *ProxyService) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	targets, err := ps.router.ResolveModel(ctx, req.Model)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, target := range targets {
		p, err := ps.providers.Get(target.ProviderID)
		if err != nil {
			lastErr = fmt.Errorf("%w: %v", gateway.ErrProviderError, err)
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
			if isClientError(err) {
				return nil, err
			}
			slog.LogAttrs(ctx, slog.LevelWarn, "provider failed, trying next",
				slog.String("provider", target.ProviderID),
				slog.String("error", err.Error()),
			)
			lastErr = fmt.Errorf("%w: %v", gateway.ErrProviderError, err)
			continue
		}
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
		p, err := ps.providers.Get(target.ProviderID)
		if err != nil {
			lastErr = fmt.Errorf("%w: %v", gateway.ErrProviderError, err)
			continue
		}

		origModel := req.Model
		req.Model = target.Model
		ch, err := p.ChatCompletionStream(ctx, req)
		req.Model = origModel
		if err != nil {
			if isClientError(err) {
				return nil, err
			}
			slog.LogAttrs(ctx, slog.LevelWarn, "provider stream failed, trying next",
				slog.String("provider", target.ProviderID),
				slog.String("error", err.Error()),
			)
			lastErr = fmt.Errorf("%w: %v", gateway.ErrProviderError, err)
			continue
		}
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
		p, err := ps.providers.Get(target.ProviderID)
		if err != nil {
			lastErr = fmt.Errorf("%w: %v", gateway.ErrProviderError, err)
			continue
		}

		origModel := req.Model
		req.Model = target.Model
		resp, err := p.Embeddings(ctx, req)
		req.Model = origModel
		if err != nil {
			if isClientError(err) {
				return nil, err
			}
			slog.LogAttrs(ctx, slog.LevelWarn, "provider embeddings failed, trying next",
				slog.String("provider", target.ProviderID),
				slog.String("error", err.Error()),
			)
			lastErr = fmt.Errorf("%w: %v", gateway.ErrProviderError, err)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
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
