// Package ollama implements the gateway.Provider and gateway.NativeProxy
// adapters for local Ollama instances.
package ollama

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
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/provider/sseutil"
)

const (
	defaultBaseURL = "http://localhost:11434"
	providerName   = "ollama"
)

// Client is an Ollama provider adapter that implements gateway.Provider
// and gateway.NativeProxy. It delegates translated (OpenAI-format) requests
// to Ollama's OpenAI-compatible endpoint and raw native requests via ProxyRequest.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// New creates an Ollama Client with a tuned http.Client.
// If baseURL is empty, it defaults to "http://localhost:11434".
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
		ForceAttemptHTTP2:   false, // Ollama is typically HTTP/1.1
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
		return nil, parseAPIError(resp)
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
		return nil, parseAPIError(resp)
	}

	ch := make(chan gateway.StreamChunk, 8)
	go c.readSSEStream(ctx, resp, ch)
	return ch, nil
}

// readSSEStream reads SSE lines from the response body and sends them as StreamChunks.
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
		ch <- gateway.StreamChunk{Err: fmt.Errorf("ollama: read stream: %w", err)}
	}
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
		return nil, parseAPIError(resp)
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

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp)
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

// apiError represents an error response from the Ollama API.
type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("ollama: HTTP %d: %s", e.StatusCode, e.Body)
}

// HTTPStatus returns the HTTP status code for failover decisions.
func (e *apiError) HTTPStatus() int { return e.StatusCode }

// parseAPIError reads the response body and returns a structured error.
func parseAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &apiError{StatusCode: resp.StatusCode, Body: string(body)}
}
