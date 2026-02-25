package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider"
)

const (
	defaultBaseURL   = "https://api.anthropic.com/v1"
	providerName     = "anthropic"
	anthropicVersion = "2023-06-01"
	bedrockVersion   = "bedrock-2023-05-31"
)

var (
	_ gateway.Provider    = (*Client)(nil)
	_ gateway.NativeProxy = (*Client)(nil)
)

// Client is an Anthropic provider adapter that implements gateway.Provider.
type Client struct {
	name    string
	baseURL string
	http    *http.Client
	hosting string // "", "vertex", "bedrock"
	region  string // cloud region (Vertex, Bedrock)
	project string // GCP project for Vertex
}

// New creates an Anthropic Client for direct API access.
// name is the instance identifier; baseURL configures the upstream.
// If baseURL is empty, it defaults to "https://api.anthropic.com/v1".
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

// NewWithHosting creates an Anthropic Client for a specific hosting platform.
// For hosting="vertex", region and project specify the GCP location.
// For hosting="bedrock", region specifies the AWS region.
func NewWithHosting(name, baseURL string, client *http.Client, hosting, region, project string) *Client {
	c := New(name, baseURL, client)
	c.hosting = hosting
	c.region = region
	c.project = project
	return c
}

// Name returns the instance identifier.
func (c *Client) Name() string { return c.name }

// Type returns the wire format identifier.
func (c *Client) Type() string { return providerName }

// ChatCompletion sends a non-streaming chat completion request to the Anthropic API.
func (c *Client) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	aReq, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: translate request: %w", err)
	}
	aReq.Stream = false

	body, err := c.marshalForHosting(aReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messagesURL(req.Model), bytes.NewReader(body))
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
		return nil, provider.ParseAPIError(providerName, resp)
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

	body, err := c.marshalForHosting(aReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.streamingURL(req.Model), bytes.NewReader(body))
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
		return nil, provider.ParseAPIError(providerName, resp)
	}

	ch := make(chan gateway.StreamChunk, 8)
	if c.hosting == "bedrock" {
		go readBedrockStream(ctx, resp.Body, ch)
	} else {
		go readStream(ctx, resp.Body, ch)
	}
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

// HealthCheck verifies connectivity to the Anthropic API by issuing a
// HEAD request to the messages endpoint. For Bedrock, issues HEAD to the
// base URL since model-specific health checks require a full invoke.
func (c *Client) HealthCheck(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodHead, c.healthURL(), nil)
	if err != nil {
		return fmt.Errorf("anthropic: health check: %w", err)
	}
	c.setHeaders(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("anthropic: health check: %w", err)
	}
	resp.Body.Close()
	return nil
}

// ProxyRequest forwards a raw HTTP request to the Anthropic API.
// It implements the gateway.NativeProxy interface.
// Bedrock uses a binary event stream protocol incompatible with SSE native proxy.
func (c *Client) ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error {
	if c.hosting == "bedrock" {
		http.Error(w, "native proxy not supported for Bedrock hosting", http.StatusNotImplemented)
		return fmt.Errorf("anthropic: native proxy not supported for bedrock")
	}
	var setAuth func(http.Header)
	if c.hosting != "vertex" {
		setAuth = func(h http.Header) {
			h.Set("anthropic-version", anthropicVersion)
		}
	}
	return provider.ForwardRequest(ctx, c.http, c.baseURL, setAuth, w, r, path)
}

// isHosted reports whether the client runs under a cloud hosting platform
// (Vertex AI or Bedrock) that requires anthropic_version in the request body.
func (c *Client) isHosted() bool {
	return c.hosting == "vertex" || c.hosting == "bedrock"
}

// setHeaders applies Anthropic-specific headers to an outbound request.
// Auth is handled by the transport chain.
func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("content-type", "application/json")
	// Direct mode: set anthropic-version header.
	// Vertex/Bedrock: anthropic_version goes in the request body instead.
	if !c.isHosted() {
		r.Header.Set("anthropic-version", anthropicVersion)
	}
}

// messagesURL returns the messages endpoint URL. For Vertex hosting, it uses
// the rawPredict endpoint. For Bedrock, it uses the model invoke endpoint.
func (c *Client) messagesURL(model string) string {
	switch c.hosting {
	case "vertex":
		return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:rawPredict",
			c.baseURL, c.project, c.region, url.PathEscape(model))
	case "bedrock":
		return fmt.Sprintf("%s/model/%s/invoke", c.baseURL, url.PathEscape(model))
	default:
		return c.baseURL + "/messages"
	}
}

// streamingURL returns the streaming endpoint URL. Bedrock uses a separate
// invoke-with-response-stream endpoint; all others share messagesURL.
func (c *Client) streamingURL(model string) string {
	if c.hosting == "bedrock" {
		return fmt.Sprintf("%s/model/%s/invoke-with-response-stream", c.baseURL, url.PathEscape(model))
	}
	return c.messagesURL(model)
}

// healthURL returns the URL for health checks. Bedrock has no model-agnostic
// messages endpoint, so we use the base URL.
func (c *Client) healthURL() string {
	if c.hosting == "bedrock" {
		return c.baseURL
	}
	return c.messagesURL("")
}

// marshalForHosting serializes an anthropicRequest. For Vertex/Bedrock hosting,
// it adds anthropic_version to the body and removes the model field.
func (c *Client) marshalForHosting(aReq *anthropicRequest) ([]byte, error) {
	if !c.isHosted() {
		return json.Marshal(aReq)
	}
	// Vertex/Bedrock: add anthropic_version in body, omit model (it's in the URL).
	type hostedRequest struct {
		AnthropicVersion string          `json:"anthropic_version"`
		MaxTokens        int             `json:"max_tokens"`
		Messages         []anthropicMsg  `json:"messages"`
		System           json.RawMessage `json:"system,omitempty"`
		Temperature      *float64        `json:"temperature,omitempty"`
		TopP             *float64        `json:"top_p,omitempty"`
		Stream           bool            `json:"stream,omitempty"`
		Tools            json.RawMessage `json:"tools,omitempty"`
		StopSeqs         json.RawMessage `json:"stop_sequences,omitempty"`
	}

	ver := anthropicVersion
	if c.hosting == "bedrock" {
		ver = bedrockVersion
	}

	hReq := hostedRequest{
		AnthropicVersion: ver,
		MaxTokens:        aReq.MaxTokens,
		Messages:         aReq.Messages,
		System:           aReq.System,
		Temperature:      aReq.Temperature,
		TopP:             aReq.TopP,
		Stream:           aReq.Stream,
		Tools:            aReq.Tools,
		StopSeqs:         aReq.StopSeqs,
	}
	return json.Marshal(hReq)
}
