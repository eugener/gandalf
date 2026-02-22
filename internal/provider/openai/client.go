// Package openai implements the gateway.Provider adapter for the OpenAI API.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/dnscache"
	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider/sseutil"
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
// If resolver is non-nil, it wraps the transport's DialContext with cached DNS lookups.
func New(apiKey, baseURL string, resolver *dnscache.Resolver) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	t := &http.Transport{
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     200,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	if resolver != nil {
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := resolver.LookupHost(ctx, host)
			if err != nil {
				return nil, err
			}
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
		}
	}

	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Transport: t},
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
		return nil, parseAPIError(resp)
	}

	ch := make(chan gateway.StreamChunk, 8)
	go c.readSSEStream(ctx, resp, ch)
	return ch, nil
}

// readSSEStream reads SSE lines from the response body and sends them as
// StreamChunks. It closes ch when done.
func (c *Client) readSSEStream(ctx context.Context, resp *http.Response, ch chan<- gateway.StreamChunk) {
	defer close(ch)
	defer resp.Body.Close()

	scanner := sseutil.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		_, data, ok := sseutil.ParseSSELine(line)
		if !ok {
			continue
		}
		if data == "[DONE]" {
			ch <- gateway.StreamChunk{Done: true}
			return
		}

		chunk := gateway.StreamChunk{Data: []byte(data)}
		// Extract usage from final chunk if present.
		if u := gjson.GetBytes(chunk.Data, "usage"); u.Exists() && u.Type == gjson.JSON {
			var usage gateway.Usage
			if json.Unmarshal([]byte(u.Raw), &usage) == nil && usage.TotalTokens > 0 {
				chunk.Usage = &usage
			}
		}

		select {
		case ch <- chunk:
		case <-ctx.Done():
			ch <- gateway.StreamChunk{Err: ctx.Err()}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- gateway.StreamChunk{Err: fmt.Errorf("openai: read stream: %w", err)}
	}
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
		return nil, parseAPIError(resp)
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

// HTTPStatus returns the HTTP status code for failover decisions.
func (e *apiError) HTTPStatus() int { return e.StatusCode }

// parseAPIError reads the response body and returns a structured error.
func parseAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &apiError{StatusCode: resp.StatusCode, Body: string(body)}
}
