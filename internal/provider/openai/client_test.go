package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
)

func TestChatCompletionStream(t *testing.T) {
	t.Parallel()

	// Canned SSE response with two content chunks + usage + [DONE].
	sseBody := "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"index\":0}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\" world\"},\"index\":0}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n" +
		"data: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		// Verify stream=true in request body.
		var req gateway.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !req.Stream {
			t.Error("stream should be true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseBody)
	}))
	defer srv.Close()

	client := New("openai", "test-key", srv.URL+"/v1", nil)
	ch, err := client.ChatCompletionStream(context.Background(), &gateway.ChatRequest{
		Model:    "gpt-4o",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var chunks []gateway.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	// Expect: 2 data chunks + 1 done
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if chunks[2].Done != true {
		t.Error("last chunk should be Done")
	}
	// Second chunk should have usage.
	if chunks[1].Usage == nil {
		t.Fatal("second chunk should have usage")
	}
	if chunks[1].Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15", chunks[1].Usage.TotalTokens)
	}
}

func TestChatCompletionStreamContextCancel(t *testing.T) {
	t.Parallel()

	// Server that sends one chunk then blocks until client disconnects.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until client context is canceled.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := New("openai", "test-key", srv.URL+"/v1", nil)
	ch, err := client.ChatCompletionStream(ctx, &gateway.ChatRequest{
		Model:    "gpt-4o",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	// Read first chunk.
	chunk := <-ch
	if len(chunk.Data) == 0 {
		t.Error("expected data in first chunk")
	}

	// Cancel context.
	cancel()

	// Drain remaining -- should get error or done.
	for c := range ch {
		if c.Err != nil {
			return // expected
		}
	}
}

func TestChatCompletionStreamHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()

	client := New("openai", "test-key", srv.URL+"/v1", nil)
	_, err := client.ChatCompletionStream(context.Background(), &gateway.ChatRequest{
		Model:    "gpt-4o",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
}

func TestChatCompletion(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing or wrong Authorization header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing Content-Type header")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(gateway.ChatResponse{
			ID:     "chatcmpl-1",
			Object: "chat.completion",
			Model:  "gpt-4o",
			Choices: []gateway.Choice{{
				Index:        0,
				Message:      gateway.Message{Role: "assistant", Content: json.RawMessage(`"Hello!"`)},
				FinishReason: "stop",
			}},
			Usage: &gateway.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		})
	}))
	defer srv.Close()

	client := New("openai-us", "test-key", srv.URL+"/v1", nil)
	resp, err := client.ChatCompletion(context.Background(), &gateway.ChatRequest{
		Model:    "gpt-4o",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", resp.Model)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 8 {
		t.Errorf("usage = %v", resp.Usage)
	}
}

func TestChatCompletionHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"internal error"}}`)
	}))
	defer srv.Close()

	client := New("openai", "test-key", srv.URL+"/v1", nil)
	_, err := client.ChatCompletion(context.Background(), &gateway.ChatRequest{
		Model:    "gpt-4o",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4o"},{"id":"gpt-3.5-turbo"}]}`)
	}))
	defer srv.Close()

	client := New("openai", "test-key", srv.URL+"/v1", nil)
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0] != "gpt-4o" {
		t.Errorf("models[0] = %q, want gpt-4o", models[0])
	}
}

func TestListModelsHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer srv.Close()

	client := New("openai", "bad-key", srv.URL+"/v1", nil)
	_, err := client.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 401")
	}
}

func TestHealthCheck(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4o"}]}`)
	}))
	defer srv.Close()

	client := New("openai", "test-key", srv.URL+"/v1", nil)
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestNameAndType(t *testing.T) {
	t.Parallel()

	client := New("openai-eu", "key", "", nil)
	if client.Name() != "openai-eu" {
		t.Errorf("Name() = %q, want openai-eu", client.Name())
	}
	if client.Type() != "openai" {
		t.Errorf("Type() = %q, want openai", client.Type())
	}
}

func TestEmbeddings(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"text-embedding-3-small","usage":{"prompt_tokens":5,"total_tokens":5}}`)
	}))
	defer srv.Close()

	client := New("openai", "test-key", srv.URL+"/v1", nil)
	resp, err := client.Embeddings(context.Background(), &gateway.EmbeddingRequest{
		Model: "text-embedding-3-small",
		Input: json.RawMessage(`"hello world"`),
	})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if resp.Model != "text-embedding-3-small" {
		t.Errorf("model = %q, want %q", resp.Model, "text-embedding-3-small")
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 5 {
		t.Error("expected usage with prompt_tokens=5")
	}
}
