package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/provider/anthropic"
	"github.com/eugener/gandalf/internal/provider/gemini"
	"github.com/eugener/gandalf/internal/provider/openai"
	"github.com/eugener/gandalf/internal/testutil"
)

// TestStreamOpenAIPassthrough verifies SSE streaming through the full stack
// with a real OpenAI-protocol upstream server.
func TestStreamOpenAIPassthrough(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w,
			"data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n"+
				"data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"!\"}}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n"+
				"data: [DONE]\n\n",
		)
	}))
	defer upstream.Close()

	h := buildHandler(t, "openai", "gpt-4o", openai.New("openai", upstream.URL+"/v1", nil))

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertSSEResponse(t, rec, "Hi", "[DONE]")
}

// TestStreamAnthropicTranslation verifies SSE streaming through the Anthropic
// adapter, confirming event-to-OpenAI-chunk translation.
func TestStreamAnthropicTranslation(t *testing.T) {
	t.Parallel()

	sseBody := "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_01","model":"claude-sonnet-4-6","usage":{"input_tokens":10}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseBody)
	}))
	defer upstream.Close()

	h := buildHandler(t, "anthropic", "claude-sonnet-4-6", anthropic.New("anthropic", upstream.URL+"/v1", nil))

	body := `{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertSSEResponse(t, rec, "Hello", "[DONE]")
}

// TestStreamGeminiEOFHandling verifies SSE streaming through the Gemini
// adapter with EOF-terminated streams (no [DONE] from upstream).
func TestStreamGeminiEOFHandling(t *testing.T) {
	t.Parallel()

	sseBody := `data: {"candidates":[{"content":{"parts":[{"text":"World"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}` + "\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseBody)
	}))
	defer upstream.Close()

	h := buildHandler(t, "gemini", "gemini-2.0-flash", gemini.New("gemini", upstream.URL+"/v1beta", nil))

	body := `{"model":"gemini-2.0-flash","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertSSEResponse(t, rec, "World", "[DONE]")
}

// TestStreamClientDisconnect verifies that the handler respects client cancellation.
func TestStreamClientDisconnect(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("fake", &testutil.FakeProvider{
		ProviderName: "fake",
		StreamFn: func(ctx context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			ch := make(chan gateway.StreamChunk, 1)
			go func() {
				defer close(ch)
				ch <- gateway.StreamChunk{Data: []byte(`{"id":"1","choices":[{"delta":{"content":"hi"}}]}`)}
				// Wait for context cancellation.
				<-ctx.Done()
				ch <- gateway.StreamChunk{Err: ctx.Err()}
			}()
			return ch, nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "test-model",
		Targets:    []byte(`[{"provider_id":"fake","model":"test-model","priority":1}]`),
		Strategy:   "priority",
	})

	routerSvc := app.NewRouterService(store)
	h := New(Deps{
		Auth:  testutil.FakeAuth{},
		Proxy: app.NewProxyService(reg, routerSvc, nil),
	})

	body := `{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")

	done := make(chan struct{})
	rec := httptest.NewRecorder()
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	// Give the handler time to start streaming then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Handler returned promptly after cancel.
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancel")
	}
}

// TestStreamProviderFailover verifies that the stream falls back to the
// secondary provider when the primary fails.
func TestStreamProviderFailover(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("primary", &testutil.FakeProvider{
		ProviderName: "primary",
		StreamFn: func(context.Context, *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return nil, errors.New("primary down")
		},
	})
	reg.Register("secondary", &testutil.FakeProvider{
		ProviderName: "secondary",
		StreamFn: func(_ context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return testutil.FakeStreamChan(
				gateway.StreamChunk{Data: []byte(`{"id":"1","choices":[{"delta":{"content":"fallback"}}]}`)},
			), nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"primary","model":"model-a","priority":1},{"provider_id":"secondary","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	routerSvc := app.NewRouterService(store)
	h := New(Deps{
		Auth:  testutil.FakeAuth{},
		Proxy: app.NewProxyService(reg, routerSvc, nil),
	})

	body := `{"model":"model-a","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assertSSEResponse(t, rec, "fallback", "[DONE]")
}

// buildHandler creates a test HTTP handler with a single provider and a
// matching route for the given model alias.
func buildHandler(t *testing.T, providerName, modelAlias string, p gateway.Provider) http.Handler {
	t.Helper()

	reg := provider.NewRegistry()
	reg.Register(providerName, p)

	store := testutil.NewFakeStore()
	targets, _ := json.Marshal([]gateway.RouteTarget{{ProviderID: providerName, Model: modelAlias, Priority: 1}})
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: modelAlias,
		Targets:    targets,
		Strategy:   "priority",
	})

	routerSvc := app.NewRouterService(store)
	return New(Deps{
		Auth:  testutil.FakeAuth{},
		Proxy: app.NewProxyService(reg, routerSvc, nil),
	})
}

// assertSSEResponse checks basic SSE response properties.
func assertSSEResponse(t *testing.T, rec *httptest.ResponseRecorder, containsText, containsSentinel string) {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, containsText) {
		t.Errorf("response missing %q, got:\n%s", containsText, body)
	}
	if !strings.Contains(body, containsSentinel) {
		t.Errorf("response missing %q, got:\n%s", containsSentinel, body)
	}
}
