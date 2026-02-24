// Package ollama implements the gateway.Provider and gateway.NativeProxy
// adapters for local Ollama instances.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/dnscache"
	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/provider/sseutil"
)

const (
	defaultBaseURL = "http://localhost:11434"
	providerName   = "ollama"
)

var (
	_ gateway.Provider    = (*Client)(nil)
	_ gateway.NativeProxy = (*Client)(nil)
)

// Client is an Ollama provider adapter that implements gateway.Provider
// and gateway.NativeProxy. It delegates translated (OpenAI-format) requests
// to Ollama's OpenAI-compatible endpoint and raw native requests via ProxyRequest.
type Client struct {
	name    string
	apiKey  string
	baseURL string
	http    *http.Client
}

// New creates an Ollama Client with a tuned http.Client.
// name is the instance identifier; apiKey and baseURL configure the upstream.
// If baseURL is empty, it defaults to "http://localhost:11434".
// If resolver is non-nil, it wraps the transport's DialContext with cached DNS lookups.
func New(name, apiKey, baseURL string, resolver *dnscache.Resolver) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		name:    name,
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Transport: provider.NewTransport(resolver, false)}, // HTTP/1.1 for local Ollama
	}
}

// Name returns the instance identifier.
func (c *Client) Name() string { return c.name }

// Type returns the wire format identifier.
func (c *Client) Type() string { return providerName }

// openaiURL returns the OpenAI-compatible API base URL for Ollama.
func (c *Client) openaiURL() string { return c.baseURL + "/v1" }

// ChatCompletion sends a non-streaming chat completion request via Ollama's
// OpenAI-compatible endpoint.
func (c *Client) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.openaiURL()+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	var out gateway.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}
	return &out, nil
}

// ChatCompletionStream sends a streaming chat completion request via Ollama's
// OpenAI-compatible endpoint.
func (c *Client) ChatCompletionStream(ctx context.Context, req *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	outReq := *req
	outReq.Stream = true

	body, err := json.Marshal(&outReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.openaiURL()+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, provider.ParseAPIError(providerName, resp)
	}

	ch := make(chan gateway.StreamChunk, 8)
	go sseutil.ReadSSEStream(ctx, providerName, resp, ch)
	return ch, nil
}

// Embeddings sends an embedding request via Ollama's OpenAI-compatible endpoint.
func (c *Client) Embeddings(ctx context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.openaiURL()+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	var out gateway.EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}
	return &out, nil
}

// ListModels returns available models from the Ollama instance.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}

	var ids []string
	gjson.ParseBytes(respBody).Get("models").ForEach(func(_, model gjson.Result) bool {
		ids = append(ids, model.Get("name").String())
		return true
	})
	return ids, nil
}

// HealthCheck verifies connectivity to the Ollama instance.
func (c *Client) HealthCheck(ctx context.Context) error {
	_, err := c.ListModels(ctx)
	return err
}

// ProxyRequest forwards a raw HTTP request to the Ollama API.
// It implements the gateway.NativeProxy interface.
func (c *Client) ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error {
	return provider.ForwardRequest(ctx, c.http, c.baseURL+"/api", func(h http.Header) {
		if c.apiKey != "" {
			h.Set("Authorization", "Bearer "+c.apiKey)
		}
	}, w, r, path)
}

// setHeaders applies common headers to an outbound request.
func (c *Client) setHeaders(r *http.Request) {
	if c.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	r.Header.Set("Content-Type", "application/json")
}
