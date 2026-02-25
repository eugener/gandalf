// Package openai implements the gateway.Provider adapter for the OpenAI API.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/provider/sseutil"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	providerName   = "openai"
)

var (
	_ gateway.Provider    = (*Client)(nil)
	_ gateway.NativeProxy = (*Client)(nil)
)

// Client is an OpenAI provider adapter that implements gateway.Provider.
type Client struct {
	name    string
	baseURL string
	http    *http.Client
	hosting string // "", "azure"
}

// New creates an OpenAI Client for direct API access.
// name is the instance identifier; baseURL configures the upstream.
// If baseURL is empty, it defaults to "https://api.openai.com/v1".
// The provided client should have auth configured via its transport chain.
func New(name, baseURL string, client *http.Client) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if client == nil {
		client = &http.Client{}
	}
	return &Client{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    client,
	}
}

// NewWithHosting creates an OpenAI Client for a specific hosting platform.
// For hosting="azure", ListModels returns the deployment model from the base URL.
func NewWithHosting(name, baseURL string, client *http.Client, hosting string) *Client {
	c := New(name, baseURL, client)
	c.hosting = hosting
	return c
}

// Name returns the instance identifier.
func (c *Client) Name() string { return c.name }

// Type returns the wire format identifier.
func (c *Client) Type() string { return providerName }

// ChatCompletion sends a non-streaming chat completion request to the OpenAI API.
func (c *Client) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	var out gateway.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	return &out, nil
}

// ChatCompletionStream sends a streaming chat completion request to the OpenAI API.
// It returns a channel of StreamChunk. The raw SSE data payloads are forwarded
// as-is in StreamChunk.Data (no JSON parsing on the hot path). The channel is
// closed after sending a Done sentinel or an error chunk.
func (c *Client) ChatCompletionStream(ctx context.Context, req *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	// Force stream=true and request usage in the final chunk.
	outReq := *req
	outReq.Stream = true
	if outReq.StreamOptions == nil {
		outReq.StreamOptions = &gateway.StreamOptions{IncludeUsage: true}
	}

	body, err := json.Marshal(&outReq)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, provider.ParseAPIError(providerName, resp)
	}

	ch := make(chan gateway.StreamChunk, 8)
	go sseutil.ReadSSEStream(ctx, providerName, resp, ch)
	return ch, nil
}

// Embeddings sends an embedding request to the OpenAI API.
func (c *Client) Embeddings(ctx context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	var out gateway.EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	return &out, nil
}

// listModelsResponse is the envelope returned by GET /models.
type listModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels returns the IDs of all models available from the OpenAI API.
// For Azure hosting, the models endpoint is not available at the deployment URL,
// so this returns a nil slice with no error.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c.hosting == "azure" {
		// Azure deployment URLs don't support GET /models.
		return nil, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	var out listModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: decode models response: %w", err)
	}

	ids := make([]string, len(out.Data))
	for i, m := range out.Data {
		ids[i] = m.ID
	}
	return ids, nil
}

// HealthCheck verifies connectivity. For Azure, sends a lightweight POST
// to check reachability since the models endpoint is not available.
func (c *Client) HealthCheck(ctx context.Context) error {
	if c.hosting == "azure" {
		// Azure deployments don't have a GET /models endpoint.
		// Verify connectivity with a HEAD request to the base URL.
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL, nil)
		if err != nil {
			return fmt.Errorf("openai: create health check request: %w", err)
		}
		resp, err := c.http.Do(httpReq)
		if err != nil {
			return fmt.Errorf("openai: health check: %w", err)
		}
		resp.Body.Close()
		return nil
	}
	_, err := c.ListModels(ctx)
	return err
}

// ProxyRequest forwards a raw HTTP request to the OpenAI API.
// It implements the gateway.NativeProxy interface.
func (c *Client) ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error {
	return provider.ForwardRequest(ctx, c.http, c.baseURL, nil, w, r, path)
}

// setHeaders applies content-type to an outbound request.
// Auth is handled by the transport chain.
func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("Content-Type", "application/json")
}
