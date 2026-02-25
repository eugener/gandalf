package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/cloudauth"
)

// testClient creates a Client with an APIKeyTransport for test assertions.
func testClient(name, key, baseURL string) *Client {
	transport := &cloudauth.APIKeyTransport{
		Key:        key,
		HeaderName: "x-api-key",
		Prefix:     "",
	}
	return New(name, baseURL, &http.Client{Transport: transport})
}

func TestTranslateRequest(t *testing.T) {
	t.Parallel()

	maxTok := 100
	req := &gateway.ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []gateway.Message{
			{Role: "system", Content: json.RawMessage(`"You are helpful."`)},
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
		MaxTokens: &maxTok,
	}

	aReq, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	if aReq.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", aReq.Model)
	}
	if aReq.MaxTokens != 100 {
		t.Errorf("max_tokens = %d, want 100", aReq.MaxTokens)
	}
	if len(aReq.Messages) != 1 {
		t.Fatalf("got %d messages, want 1 (system extracted)", len(aReq.Messages))
	}
	if aReq.System == nil {
		t.Error("system should be set")
	}
	if aReq.Messages[0].Role != "user" {
		t.Errorf("message role = %q, want user", aReq.Messages[0].Role)
	}
}

func TestTranslateResponse(t *testing.T) {
	t.Parallel()

	data := []byte(`{
		"id": "msg_01",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-6",
		"content": [{"type": "text", "text": "Hello!"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)

	resp, err := translateResponse(data)
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	if resp.ID != "msg_01" {
		t.Errorf("id = %q", resp.ID)
	}
	if resp.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("usage total_tokens = %v", resp.Usage)
	}
}

func TestChatCompletion(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Error("missing x-api-key")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("missing anthropic-version")
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_01",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-4-6",
			"content": [{"type": "text", "text": "Hi!"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`)
	}))
	defer srv.Close()

	client := testClient("anthropic", "test-key", srv.URL+"/v1")
	resp, err := client.ChatCompletion(context.Background(), &gateway.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.ID != "msg_01" {
		t.Errorf("id = %q, want msg_01", resp.ID)
	}
}

func TestChatCompletionStream(t *testing.T) {
	t.Parallel()

	// Simulate Anthropic SSE events.
	sseBody := "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_01","model":"claude-sonnet-4-6","usage":{"input_tokens":10}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseBody)
	}))
	defer srv.Close()

	client := testClient("anthropic", "test-key", srv.URL+"/v1")
	ch, err := client.ChatCompletionStream(context.Background(), &gateway.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []gateway.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var chunks []gateway.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	// Expect: role chunk, 2 text deltas, finish chunk, usage chunk, done
	if len(chunks) < 4 {
		t.Fatalf("got %d chunks, want at least 4", len(chunks))
	}

	// Last chunk should be Done.
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Error("last chunk should be Done")
	}

	// Second-to-last should have usage.
	usageChunk := chunks[len(chunks)-2]
	if usageChunk.Usage == nil {
		t.Fatal("expected usage in second-to-last chunk")
	}
	if usageChunk.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15", usageChunk.Usage.TotalTokens)
	}
}

func TestMapStopReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"end_turn", "stop"},
		{"max_tokens", "length"},
		{"tool_use", "tool_calls"},
		{"stop_sequence", "stop"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		if got := mapStopReason(tt.in); got != tt.want {
			t.Errorf("mapStopReason(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestVertexMessagesURL(t *testing.T) {
	t.Parallel()

	c := NewWithHosting("vertex-claude", "https://us-central1-aiplatform.googleapis.com",
		&http.Client{}, "vertex", "us-central1", "my-project")

	got := c.messagesURL("claude-sonnet-4-6")
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/publishers/anthropic/models/claude-sonnet-4-6:rawPredict"
	if got != want {
		t.Errorf("messagesURL =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestVertexMarshalForHosting(t *testing.T) {
	t.Parallel()

	c := NewWithHosting("vertex-claude", "https://example.com",
		&http.Client{}, "vertex", "us-central1", "proj")

	aReq := &anthropicRequest{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 1024,
		Messages:  []anthropicMsg{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}

	body, err := c.marshalForHosting(aReq)
	if err != nil {
		t.Fatalf("marshalForHosting: %v", err)
	}

	bodyStr := string(body)
	// Should have anthropic_version in body.
	if !strings.Contains(bodyStr, `"anthropic_version":"2023-06-01"`) {
		t.Error("body should contain anthropic_version")
	}
	// Should NOT have model field in body (it's in the URL).
	if strings.Contains(bodyStr, `"model"`) {
		t.Error("body should not contain model field for Vertex")
	}
}

func TestVertexSetHeadersSkipsVersion(t *testing.T) {
	t.Parallel()

	c := NewWithHosting("vertex-claude", "https://example.com",
		&http.Client{}, "vertex", "us-central1", "proj")

	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	c.setHeaders(req)

	if req.Header.Get("anthropic-version") != "" {
		t.Error("Vertex mode should not set anthropic-version header")
	}
	if req.Header.Get("content-type") != "application/json" {
		t.Error("should set content-type")
	}
}

func TestDirectModeSetHeaders(t *testing.T) {
	t.Parallel()

	c := New("anthropic", "", nil)

	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	c.setHeaders(req)

	if req.Header.Get("anthropic-version") != "2023-06-01" {
		t.Error("direct mode should set anthropic-version header")
	}
}
