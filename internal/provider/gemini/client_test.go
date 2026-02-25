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
	"github.com/eugener/gandalf/internal/cloudauth"
)

// testClient creates a Client with an APIKeyTransport for test assertions.
func testClient(name, key, baseURL string) *Client {
	transport := &cloudauth.APIKeyTransport{
		Key:        key,
		HeaderName: "x-goog-api-key",
		Prefix:     "",
	}
	return New(name, baseURL, &http.Client{Transport: transport})
}

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
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Error("missing API key in x-goog-api-key header")
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

	client := testClient("gemini", "test-key", srv.URL+"/v1beta")
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

	client := testClient("gemini", "test-key", srv.URL+"/v1beta")
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

func TestEmbeddings(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":embedContent") {
			t.Errorf("path = %s, want :embedContent", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"embedding":{"values":[0.1,0.2,0.3]}}`)
	}))
	defer srv.Close()

	client := testClient("gemini", "test-key", srv.URL+"/v1beta")
	resp, err := client.Embeddings(context.Background(), &gateway.EmbeddingRequest{
		Model: "text-embedding-004",
		Input: json.RawMessage(`"hello world"`),
	})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
	if resp.Model != "text-embedding-004" {
		t.Errorf("model = %q", resp.Model)
	}
}

func TestEmbeddingsArrayInput(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"embedding":{"values":[0.4,0.5]}}`)
	}))
	defer srv.Close()

	client := testClient("gemini", "test-key", srv.URL+"/v1beta")
	resp, err := client.Embeddings(context.Background(), &gateway.EmbeddingRequest{
		Model: "text-embedding-004",
		Input: json.RawMessage(`["hello","world"]`),
	})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
}

func TestEmbeddingsHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad request"}}`)
	}))
	defer srv.Close()

	client := testClient("gemini", "test-key", srv.URL+"/v1beta")
	_, err := client.Embeddings(context.Background(), &gateway.EmbeddingRequest{
		Model: "text-embedding-004",
		Input: json.RawMessage(`"hello"`),
	})
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("path = %s, want /v1beta/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"name":"models/gemini-2.0-flash"},{"name":"models/gemini-1.5-pro"}]}`)
	}))
	defer srv.Close()

	client := testClient("gemini", "test-key", srv.URL+"/v1beta")
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	// Verify "models/" prefix is stripped.
	if models[0] != "gemini-2.0-flash" {
		t.Errorf("models[0] = %q, want gemini-2.0-flash", models[0])
	}
}

func TestListModelsHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"message":"forbidden"}}`)
	}))
	defer srv.Close()

	client := testClient("gemini", "bad-key", srv.URL+"/v1beta")
	_, err := client.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for HTTP 403")
	}
}

func TestHealthCheck(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"name":"models/gemini-2.0-flash"}]}`)
	}))
	defer srv.Close()

	client := testClient("gemini", "test-key", srv.URL+"/v1beta")
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
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

func TestVertexGenerateURL(t *testing.T) {
	t.Parallel()

	c := NewWithHosting("vertex-gemini", "https://us-central1-aiplatform.googleapis.com",
		&http.Client{}, "vertex", "us-central1", "my-project")

	got := c.generateURL("gemini-2.0-flash", "generateContent")
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-2.0-flash:generateContent"
	if got != want {
		t.Errorf("generateURL =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestVertexModelsURL(t *testing.T) {
	t.Parallel()

	c := NewWithHosting("vertex-gemini", "https://us-central1-aiplatform.googleapis.com",
		&http.Client{}, "vertex", "us-central1", "my-project")

	got := c.modelsURL()
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/publishers/google/models"
	if got != want {
		t.Errorf("modelsURL =\n  %s\nwant:\n  %s", got, want)
	}
}

func TestDirectGenerateURL(t *testing.T) {
	t.Parallel()

	c := New("gemini", "https://generativelanguage.googleapis.com/v1beta", nil)
	got := c.generateURL("gemini-2.0-flash", "generateContent")
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
	if got != want {
		t.Errorf("generateURL =\n  %s\nwant:\n  %s", got, want)
	}
}
