package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/cloudauth"
)

// testClientWithKey creates a Client with an APIKeyTransport for test assertions.
func testClientWithKey(name, key, baseURL string) *Client {
	transport := &cloudauth.APIKeyTransport{
		Key:        key,
		HeaderName: "Authorization",
		Prefix:     "Bearer ",
	}
	return New(name, baseURL, &http.Client{Transport: transport})
}

func TestChatCompletion(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing Content-Type header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gateway.ChatResponse{
			ID:     "chatcmpl-ollama",
			Object: "chat.completion",
			Model:  "llama3",
			Choices: []gateway.Choice{{
				Index:        0,
				Message:      gateway.Message{Role: "assistant", Content: json.RawMessage(`"hello"`)},
				FinishReason: "stop",
			}},
		})
	}))
	defer ts.Close()

	c := New("ollama", ts.URL, nil)
	resp, err := c.ChatCompletion(context.Background(), &gateway.ChatRequest{
		Model:    "llama3",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "llama3" {
		t.Errorf("model = %q, want llama3", resp.Model)
	}
}

func TestChatCompletionStream(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, line := range []string{
			`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"hi"}}]}`,
			`data: [DONE]`,
		} {
			io.WriteString(w, line+"\n\n")
			flusher.Flush()
		}
	}))
	defer ts.Close()

	c := New("ollama", ts.URL, nil)
	ch, err := c.ChatCompletionStream(context.Background(), &gateway.ChatRequest{
		Model:    "llama3",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Stream:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var chunks int
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		chunks++
		if chunk.Done {
			break
		}
	}
	if chunks < 2 {
		t.Errorf("got %d chunks, want >= 2", chunks)
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"models":[{"name":"llama3"},{"name":"mistral"}]}`)
	}))
	defer ts.Close()

	c := New("ollama", ts.URL, nil)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0] != "llama3" || models[1] != "mistral" {
		t.Errorf("models = %v", models)
	}
}

func TestEmbeddings(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q, want /v1/embeddings", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"nomic-embed-text","usage":{"prompt_tokens":3,"total_tokens":3}}`)
	}))
	defer ts.Close()

	c := New("ollama", ts.URL, nil)
	resp, err := c.Embeddings(context.Background(), &gateway.EmbeddingRequest{
		Model: "nomic-embed-text",
		Input: json.RawMessage(`"hello"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "nomic-embed-text" {
		t.Errorf("model = %q, want nomic-embed-text", resp.Model)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 3 {
		t.Error("expected usage with prompt_tokens=3")
	}
}

func TestEmbeddingsHTTPError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"unsupported model"}`)
	}))
	defer ts.Close()

	c := New("ollama", ts.URL, nil)
	_, err := c.Embeddings(context.Background(), &gateway.EmbeddingRequest{
		Model: "bad-model",
		Input: json.RawMessage(`"hello"`),
	})
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, want HTTP 400", err)
	}
}

func TestHTTPError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":"boom"}`)
	}))
	defer ts.Close()

	c := New("ollama", ts.URL, nil)
	_, err := c.ChatCompletion(context.Background(), &gateway.ChatRequest{
		Model:    "llama3",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want HTTP 500", err)
	}
}

func TestProxyRequest(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body) // echo back
	}))
	defer ts.Close()

	c := testClientWithKey("ollama", "test-key", ts.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"model":"llama3"}`))
	req.Header.Set("Content-Type", "application/json")

	err := c.ProxyRequest(context.Background(), rec, req, "/chat")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "llama3") {
		t.Errorf("body = %q, want to contain llama3", rec.Body.String())
	}
}

func TestHealthCheck(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"models":[{"name":"llama3"}]}`)
	}))
	defer ts.Close()

	c := New("ollama", ts.URL, nil)
	if err := c.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestHealthCheckError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	ts.Close() // close immediately

	c := New("ollama", ts.URL, nil)
	if err := c.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestName(t *testing.T) {
	t.Parallel()
	c := New("ollama", "", nil)
	if c.Name() != "ollama" {
		t.Errorf("Name() = %q, want ollama", c.Name())
	}
}

func TestDefaultBaseURL(t *testing.T) {
	t.Parallel()
	c := New("ollama", "", nil)
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
}
