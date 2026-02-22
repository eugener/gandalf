// Package openai implements the gateway.Provider adapter for the OpenAI API.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	providerName   = "openai"
)

// Client is an OpenAI provider adapter that implements gateway.Provider.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// New creates an OpenAI Client with a tuned http.Client.
// If baseURL is empty, it defaults to "https://api.openai.com/v1".
func New(apiKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http: &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost:   100,
				MaxConnsPerHost:       200,
				IdleConnTimeout:       90 * time.Second,
				ForceAttemptHTTP2:     true,
				TLSHandshakeTimeout:   5 * time.Second,
			},
		},
	}
}

// Name returns the provider identifier.
func (c *Client) Name() string { return providerName }

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
		return nil, parseAPIError(resp)
	}

	var out gateway.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	return &out, nil
}

// ChatCompletionStream is not yet implemented.
func (c *Client) ChatCompletionStream(_ context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	return nil, fmt.Errorf("openai: ChatCompletionStream not implemented yet")
}

// Embeddings is not yet implemented.
func (c *Client) Embeddings(_ context.Context, _ *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	return nil, fmt.Errorf("openai: Embeddings not implemented yet")
}

// listModelsResponse is the envelope returned by GET /models.
type listModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels returns the IDs of all models available from the OpenAI API.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
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
		return nil, parseAPIError(resp)
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

// HealthCheck verifies connectivity by listing models.
func (c *Client) HealthCheck(ctx context.Context) error {
	_, err := c.ListModels(ctx)
	return err
}

// setHeaders applies common headers (auth + content-type) to an outbound request.
func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+c.apiKey)
	r.Header.Set("Content-Type", "application/json")
}

// apiError represents an error response from the OpenAI API.
type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("openai: HTTP %d: %s", e.StatusCode, e.Body)
}

// parseAPIError reads the response body and returns a structured error.
func parseAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &apiError{StatusCode: resp.StatusCode, Body: string(body)}
}
