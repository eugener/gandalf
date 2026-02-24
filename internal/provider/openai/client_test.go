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
