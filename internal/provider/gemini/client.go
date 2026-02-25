package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	name    string
	baseURL string
	http    *http.Client
	hosting string // "", "vertex"
	region  string // GCP region for Vertex
	project string // GCP project for Vertex
}

// New creates a Gemini Client for direct API access.
// name is the instance identifier; baseURL configures the upstream.
// If baseURL is empty, it defaults to the Gemini API endpoint.
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

// NewWithHosting creates a Gemini Client for a specific hosting platform.
// For hosting="vertex", region and project specify the GCP location.
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

// ChatCompletion sends a non-streaming chat completion request to the Gemini API.
func (c *Client) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	gReq := translateRequest(req)

	body, err := json.Marshal(gReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}

	u := c.generateURL(req.Model, "generateContent")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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

	u := c.generateURL(req.Model, "streamGenerateContent") + "?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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

	u := c.generateURL(req.Model, "embedContent")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
	u := c.modelsURL()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}

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
	parsed := gjson.ParseBytes(respBody)

	// Vertex AI uses "publisherModels" key; direct API uses "models".
	modelsKey := "models"
	if c.hosting == "vertex" {
		modelsKey = "publisherModels"
	}

	parsed.Get(modelsKey).ForEach(func(_, model gjson.Result) bool {
		name := model.Get("name").String()
		// Strip common prefixes (direct: "models/", Vertex: "publishers/google/models/").
		for _, prefix := range []string{"publishers/google/models/", "models/"} {
			if after, ok := strings.CutPrefix(name, prefix); ok {
				name = after
				break
			}
		}
		ids = append(ids, name)
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
	return provider.ForwardRequest(ctx, c.http, c.baseURL, nil, w, r, path)
}

// generateURL returns the endpoint URL for a model action (generateContent,
// streamGenerateContent, embedContent). For Vertex, it uses the publisher path.
func (c *Client) generateURL(model, action string) string {
	if c.hosting == "vertex" {
		return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:%s",
			c.baseURL, c.project, c.region, model, action)
	}
	return fmt.Sprintf("%s/models/%s:%s", c.baseURL, model, action)
}

// modelsURL returns the models listing endpoint URL.
func (c *Client) modelsURL() string {
	if c.hosting == "vertex" {
		return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models",
			c.baseURL, c.project, c.region)
	}
	return c.baseURL + "/models"
}

