package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
)

func TestTranslateRequest(t *testing.T) {
	t.Parallel()

	maxTok := 100
	req := &gateway.ChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []gateway.Message{
			{Role: "system", Content: json.RawMessage(`"You are helpful."`)},
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
			{Role: "assistant", Content: json.RawMessage(`"Hi there"`)},
		},
		MaxTokens: &maxTok,
	}

	gReq := translateRequest(req)
	if gReq.SystemInstruction == nil {
		t.Fatal("system instruction should be set")
	}
	if len(gReq.Contents) != 2 {
		t.Fatalf("got %d contents, want 2", len(gReq.Contents))
	}
	if gReq.Contents[0].Role != "user" {
		t.Errorf("contents[0].role = %q, want user", gReq.Contents[0].Role)
	}
	if gReq.Contents[1].Role != "model" {
		t.Errorf("contents[1].role = %q, want model", gReq.Contents[1].Role)
	}
	if gReq.GenerationConfig == nil || *gReq.GenerationConfig.MaxOutputTokens != 100 {
		t.Error("max_output_tokens should be 100")
	}
}

func TestTranslateResponse(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"candidates": [{
			"content": {"parts": [{"text": "Hello!"}]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 10,
			"candidatesTokenCount": 5,
			"totalTokenCount": 15
		}
	}`)

	resp, err := translateResponse(data, "gemini-2.0-flash")
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	if resp.Model != "gemini-2.0-flash" {
		t.Errorf("model = %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("usage = %v", resp.Usage)
	}
}

func TestChatCompletion(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("path = %s, want :generateContent", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Error("missing API key in query params")
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"candidates": [{
				"content": {"parts": [{"text": "Hi!"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 2, "totalTokenCount": 7}
		}`)
	}))
	defer srv.Close()

	client := New("test-key", srv.URL+"/v1beta", nil)
	resp, err := client.ChatCompletion(context.Background(), &gateway.ChatRequest{
		Model:    "gemini-2.0-flash",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 7 {
		t.Errorf("usage = %v", resp.Usage)
	}
}

func TestChatCompletionStreamEOFTerminated(t *testing.T) {
	t.Parallel()

	// Gemini streaming: data lines only, no event field, no [DONE], EOF-terminated.
	sseBody := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseBody)
	}))
	defer srv.Close()

	client := New("test-key", srv.URL+"/v1beta", nil)
	ch, err := client.ChatCompletionStream(context.Background(), &gateway.ChatRequest{
		Model:    "gemini-2.0-flash",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var chunks []gateway.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	// Expect: 2 text chunks + 1 usage chunk + 1 done
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want at least 3", len(chunks))
	}

	// Last should be Done.
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Error("last chunk should be Done")
	}

	// Second-to-last should have usage (cumulative).
	usageChunk := chunks[len(chunks)-2]
	if usageChunk.Usage == nil {
		t.Fatal("expected usage chunk")
	}
	if usageChunk.Usage.TotalTokens != 8 {
		t.Errorf("total_tokens = %d, want 8", usageChunk.Usage.TotalTokens)
	}
}

func TestMapStopReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"STOP", "stop"},
		{"MAX_TOKENS", "length"},
		{"SAFETY", "content_filter"},
		{"RECITATION", "content_filter"},
		{"UNKNOWN", "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := mapStopReason(tt.in); got != tt.want {
			t.Errorf("mapStopReason(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
