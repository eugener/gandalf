package app

import (
	"context"
	"fmt"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider"
)

// ProxyService forwards chat completion requests to the appropriate LLM provider
// based on model routing configuration.
type ProxyService struct {
	providers *provider.Registry
	router    *RouterService
}

// NewProxyService returns a ProxyService wired to the given provider registry and router.
func NewProxyService(providers *provider.Registry, router *RouterService) *ProxyService {
	return &ProxyService{providers: providers, router: router}
}

// ChatCompletion resolves the requested model to a provider via routing rules
// and forwards the chat completion request. Streaming is not supported yet;
// if req.Stream is true the upstream still returns a non-streaming response.
func (ps *ProxyService) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	providerName, actualModel, err := ps.router.ResolveModel(ctx, req.Model)
	if err != nil {
		return nil, err
	}

	p, err := ps.providers.Get(providerName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", gateway.ErrProviderError, err)
	}

	// Shallow copy to avoid mutating caller's request.
	outReq := *req
	outReq.Model = actualModel

	return p.ChatCompletion(ctx, &outReq)
}
