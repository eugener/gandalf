package gemini

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
)

const (
	defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	providerName   = "gemini"
)

var (
	_ gateway.Provider    = (*Client)(nil)
	_ gateway.NativeProxy = (*Client)(nil)
)

// Client is a Gemini provider adapter that implements gateway.Provider.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// New creates a Gemini Client with a tuned http.Client.
// If baseURL is empty, it defaults to the Gemini API endpoint.
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

// ChatCompletion sends a non-streaming chat completion request to the Gemini API.
func (c *Client) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	gReq := translateRequest(req)

	body, err := json.Marshal(gReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	u := fmt.Sprintf("%s/models/%s:generateContent", c.baseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("gemini: read response: %w", err)
	}

	return translateResponse(respBody, req.Model)
}

// ChatCompletionStream sends a streaming chat completion request to the Gemini API.
func (c *Client) ChatCompletionStream(ctx context.Context, req *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	gReq := translateRequest(req)

	body, err := json.Marshal(gReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	u := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", c.baseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, provider.ParseAPIError(providerName, resp)
	}

	ch := make(chan gateway.StreamChunk, 8)
	go readStream(ctx, resp.Body, ch, req.Model)
	return ch, nil
}

// Embeddings sends an embedding request to the Gemini API.
func (c *Client) Embeddings(ctx context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	// Extract text from the input field.
	var inputText string
	if err := json.Unmarshal(req.Input, &inputText); err != nil {
		// Try as array of strings.
		var inputs []string
		if err := json.Unmarshal(req.Input, &inputs); err != nil {
			return nil, fmt.Errorf("gemini: unsupported input format: %w", err)
		}
		if len(inputs) > 0 {
			inputText = inputs[0]
		}
	}

	gReq := map[string]any{
		"model": "models/" + req.Model,
		"content": map[string]any{
			"parts": []map[string]any{{"text": inputText}},
		},
	}

	body, err := json.Marshal(gReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	u := fmt.Sprintf("%s/models/%s:embedContent", c.baseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("gemini: read response: %w", err)
	}

	// Convert Gemini embedding response to OpenAI format.
	r := gjson.ParseBytes(respBody)
	embValues := r.Get("embedding.values").Raw

	embData, _ := json.Marshal([]map[string]any{{
		"object":    "embedding",
		"index":     0,
		"embedding": json.RawMessage(embValues),
	}})

	return &gateway.EmbeddingResponse{
		Object: "list",
		Data:   embData,
		Model:  req.Model,
	}, nil
}

// ListModels returns the available Gemini model IDs.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	u := fmt.Sprintf("%s/models", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, provider.ParseAPIError(providerName, resp)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("gemini: read response: %w", err)
	}

	var ids []string
	gjson.ParseBytes(respBody).Get("models").ForEach(func(_, model gjson.Result) bool {
		name := model.Get("name").String()
		// Strip "models/" prefix.
		if after, ok := strings.CutPrefix(name, "models/"); ok {
			ids = append(ids, after)
		} else {
			ids = append(ids, name)
		}
		return true
	})
	return ids, nil
}

// HealthCheck verifies connectivity to the Gemini API.
func (c *Client) HealthCheck(ctx context.Context) error {
	_, err := c.ListModels(ctx)
	return err
}

// ProxyRequest forwards a raw HTTP request to the Gemini API.
// It implements the gateway.NativeProxy interface.
func (c *Client) ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error {
	return provider.ForwardRequest(ctx, c.http, c.baseURL, func(h http.Header) {
		h.Set("x-goog-api-key", c.apiKey)
	}, w, r, path)
}

