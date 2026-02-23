package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/dnscache"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider"
)

const (
	defaultBaseURL   = "https://api.anthropic.com/v1"
	providerName     = "anthropic"
	anthropicVersion = "2023-06-01"
)

var (
	_ gateway.Provider    = (*Client)(nil)
	_ gateway.NativeProxy = (*Client)(nil)
)

// Client is an Anthropic provider adapter that implements gateway.Provider.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// New creates an Anthropic Client with a tuned http.Client.
// If baseURL is empty, it defaults to "https://api.anthropic.com/v1".
// If resolver is non-nil, it wraps the transport's DialContext with cached DNS lookups.
func New(apiKey, baseURL string, resolver *dnscache.Resolver) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Transport: provider.NewTransport(resolver, true)},
	}
}

// Name returns the provider identifier.
func (c *Client) Name() string { return providerName }

// ChatCompletion sends a non-streaming chat completion request to the Anthropic API.
func (c *Client) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	aReq, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: translate request: %w", err)
	}
	aReq.Stream = false

	body, err := json.Marshal(aReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	return translateResponse(respBody)
}

// ChatCompletionStream sends a streaming chat completion request to the Anthropic API.
func (c *Client) ChatCompletionStream(ctx context.Context, req *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	aReq, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: translate request: %w", err)
	}
	aReq.Stream = true

	body, err := json.Marshal(aReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, parseAPIError(resp)
	}

	ch := make(chan gateway.StreamChunk, 8)
	go readStream(ctx, resp.Body, ch)
	return ch, nil
}

// Embeddings is not supported by Anthropic.
func (c *Client) Embeddings(_ context.Context, _ *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	return nil, fmt.Errorf("anthropic: embeddings not supported")
}

// ListModels returns the known Anthropic model IDs.
// Anthropic does not have a public models endpoint, so we return a static list.
func (c *Client) ListModels(_ context.Context) ([]string, error) {
	return []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
	}, nil
}

// HealthCheck verifies connectivity to the Anthropic API.
func (c *Client) HealthCheck(ctx context.Context) error {
	// Use a minimal messages request to check connectivity.
	_, err := c.ListModels(ctx)
	return err
}

// ProxyRequest forwards a raw HTTP request to the Anthropic API.
// It implements the gateway.NativeProxy interface.
func (c *Client) ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error {
	return provider.ForwardRequest(ctx, c.http, c.baseURL, func(h http.Header) {
		h.Set("x-api-key", c.apiKey)
		h.Set("anthropic-version", anthropicVersion)
	}, w, r, path)
}

// setHeaders applies Anthropic-specific headers to an outbound request.
func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("x-api-key", c.apiKey)
	r.Header.Set("anthropic-version", anthropicVersion)
	r.Header.Set("content-type", "application/json")
}

// apiError represents an error response from the Anthropic API.
type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("anthropic: HTTP %d: %s", e.StatusCode, e.Body)
}

// HTTPStatus returns the HTTP status code for failover decisions.
func (e *apiError) HTTPStatus() int { return e.StatusCode }

// parseAPIError reads the response body and returns a structured error.
func parseAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &apiError{StatusCode: resp.StatusCode, Body: string(body)}
}
