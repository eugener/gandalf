package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/cache"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/ratelimit"
	"github.com/eugener/gandalf/internal/tokencount"
)

// fakeAuth always authenticates successfully.
type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, _ *http.Request) (*gateway.Identity, error) {
	return &gateway.Identity{
		Subject:    "test",
		KeyID:      "key-test-1",
		OrgID:      "default",
		Role:       "admin",
		Perms:      gateway.RolePermissions["admin"],
		AuthMethod: "apikey",
	}, nil
}

// fakeProvider returns a canned response.
type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) ChatCompletion(_ context.Context, _ *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	return &gateway.ChatResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4o",
		Choices: []gateway.Choice{{
			Index:        0,
			Message:      gateway.Message{Role: "assistant", Content: []byte(`"Hello!"`)},
			FinishReason: "stop",
		}},
	}, nil
}
func (fakeProvider) ChatCompletionStream(_ context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	ch := make(chan gateway.StreamChunk, 3)
	ch <- gateway.StreamChunk{Data: []byte(`{"id":"chatcmpl-test","choices":[{"delta":{"content":"hi"}}]}`)}
	ch <- gateway.StreamChunk{Data: []byte(`{"id":"chatcmpl-test","choices":[{"delta":{"content":"!"}}]}`)}
	ch <- gateway.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}
func (fakeProvider) Embeddings(_ context.Context, _ *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	return &gateway.EmbeddingResponse{
		Object: "list",
		Data:   []byte(`[{"object":"embedding","index":0,"embedding":[0.1]}]`),
		Model:  "text-embedding-3-small",
		Usage:  &gateway.Usage{PromptTokens: 3, TotalTokens: 3},
	}, nil
}
func (fakeProvider) ListModels(context.Context) ([]string, error) { return []string{"gpt-4o"}, nil }
func (fakeProvider) HealthCheck(context.Context) error             { return nil }

func newTestHandler() http.Handler {
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})

	routerSvc := app.NewRouterService(&fakeRouteStore{})
	return New(Deps{
		Auth:      fakeAuth{},
		Proxy:     app.NewProxyService(reg, routerSvc),
		Providers: reg,
		Router:    routerSvc,
	})
}

// fakeRouteStore returns a route that maps to our fake provider.
type fakeRouteStore struct{}

func (fakeRouteStore) CreateRoute(context.Context, *gateway.Route) error { return nil }
func (fakeRouteStore) GetRoute(context.Context, string) (*gateway.Route, error) {
	return nil, gateway.ErrNotFound
}
func (fakeRouteStore) GetRouteByAlias(_ context.Context, alias string) (*gateway.Route, error) {
	return &gateway.Route{
		ID:         "r-1",
		ModelAlias: alias,
		Targets:    []byte(`[{"provider_id":"fake","model":"gpt-4o","priority":1}]`),
		Strategy:   "priority",
	}, nil
}
func (fakeRouteStore) ListRoutes(context.Context) ([]*gateway.Route, error) { return nil, nil }
func (fakeRouteStore) CountRoutes(context.Context) (int, error)             { return 0, nil }
func (fakeRouteStore) UpdateRoute(context.Context, *gateway.Route) error    { return nil }
func (fakeRouteStore) DeleteRoute(context.Context, string) error            { return nil }

func TestHealthz(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestChatCompletion(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "chatcmpl-test") {
		t.Errorf("body missing expected id, got: %s", rec.Body.String())
	}
}

func TestChatCompletionNoAuth(t *testing.T) {
	t.Parallel()

	// Create handler with an auth that rejects
	reg := provider.NewRegistry()
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	h := New(Deps{
		Auth:  rejectAuth{},
		Proxy: app.NewProxyService(reg, routerSvc),
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

type rejectAuth struct{}

func (rejectAuth) Authenticate(context.Context, *http.Request) (*gateway.Identity, error) {
	return nil, gateway.ErrUnauthorized
}

func TestReadyz(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestReadyzFailing(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	h := New(Deps{
		Auth:  fakeAuth{},
		Proxy: app.NewProxyService(reg, routerSvc),
		ReadyCheck: func(context.Context) error {
			return errors.New("db down")
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRequestIDHeader(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header should be set")
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gpt-4o") {
		t.Errorf("body missing gpt-4o, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"list"`) {
		t.Error("response should be an object list")
	}
}

func TestEmbeddings(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	body := `{"model":"text-embedding-3-small","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "text-embedding-3-small") {
		t.Errorf("body missing model, got: %s", rec.Body.String())
	}
}

func TestChatCompletionStream(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	respBody := rec.Body.String()
	if !strings.Contains(respBody, "data: ") {
		t.Error("response should contain SSE data frames")
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Error("response should contain [DONE] sentinel")
	}
}

// rateLimitAuth returns identity with rate limits configured.
type rateLimitAuth struct {
	rpm int64
	tpm int64
}

func (a rateLimitAuth) Authenticate(_ context.Context, _ *http.Request) (*gateway.Identity, error) {
	return &gateway.Identity{
		Subject:    "test",
		KeyID:      "key-rl-1",
		OrgID:      "default",
		Role:       "admin",
		Perms:      gateway.RolePermissions["admin"],
		AuthMethod: "apikey",
		RPMLimit:   a.rpm,
		TPMLimit:   a.tpm,
	}, nil
}

func TestRateLimit_RPMAllowed(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	rl := ratelimit.NewRegistry()

	h := New(Deps{
		Auth:        rateLimitAuth{rpm: 10},
		Proxy:       app.NewProxyService(reg, routerSvc),
		Providers:   reg,
		Router:      routerSvc,
		RateLimiter: rl,
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Header().Get("X-Ratelimit-Limit-Requests") != "10" {
		t.Errorf("limit header = %q, want 10", rec.Header().Get("X-Ratelimit-Limit-Requests"))
	}
}

func TestRateLimit_RPMDenied(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	rl := ratelimit.NewRegistry()

	h := New(Deps{
		Auth:        rateLimitAuth{rpm: 1},
		Proxy:       app.NewProxyService(reg, routerSvc),
		Providers:   reg,
		Router:      routerSvc,
		RateLimiter: rl,
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer gnd_test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code == http.StatusTooManyRequests {
			if rec.Header().Get("Retry-After") == "" {
				t.Error("Retry-After header should be set on 429")
			}
			return // success
		}
	}
	t.Error("expected 429 after exceeding RPM limit")
}

// capturingRecorder captures usage records.
type capturingRecorder struct {
	mu      sync.Mutex
	records []gateway.UsageRecord
}

func (c *capturingRecorder) Record(r gateway.UsageRecord) {
	c.mu.Lock()
	c.records = append(c.records, r)
	c.mu.Unlock()
}

func TestUsageRecording(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	usage := &capturingRecorder{}

	h := New(Deps{
		Auth:      fakeAuth{},
		Proxy:     app.NewProxyService(reg, routerSvc),
		Providers: reg,
		Router:    routerSvc,
		Usage:     usage,
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.records) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(usage.records))
	}
	r := usage.records[0]
	if r.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", r.Model)
	}
	if r.KeyID != "key-test-1" {
		t.Errorf("key_id = %q, want key-test-1", r.KeyID)
	}
}

// newTestHandlerWith creates a handler with custom deps merged on top of defaults.
func newTestHandlerWith(fn func(*Deps)) http.Handler {
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	deps := Deps{
		Auth:      fakeAuth{},
		Proxy:     app.NewProxyService(reg, routerSvc),
		Providers: reg,
		Router:    routerSvc,
	}
	if fn != nil {
		fn(&deps)
	}
	return New(deps)
}

func TestRateLimit_TPMDenied(t *testing.T) {
	t.Parallel()
	rl := ratelimit.NewRegistry()
	h := New(Deps{
		Auth:         rateLimitAuth{rpm: 1000, tpm: 1},
		Proxy:        app.NewProxyService(provider.NewRegistry(), app.NewRouterService(&fakeRouteStore{})),
		RateLimiter:  rl,
		TokenCounter: tokencount.NewCounter(),
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello world this is a long message to exceed one token"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Ratelimit-Limit-Tokens") == "" {
		t.Error("X-Ratelimit-Limit-Tokens header should be set")
	}
}

func TestRateLimit_QuotaExceeded(t *testing.T) {
	t.Parallel()
	qt := ratelimit.NewQuotaTracker()
	qt.Consume("key-rl-1", 100) // exceed budget

	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	h := New(Deps{
		Auth: func() gateway.Authenticator {
			return quotaAuth{maxBudget: 10}
		}(),
		Proxy:     app.NewProxyService(reg, routerSvc),
		Providers: reg,
		Router:    routerSvc,
		Quota:     qt,
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "quota exceeded") {
		t.Errorf("body should contain 'quota exceeded', got: %s", rec.Body.String())
	}
}

type quotaAuth struct {
	maxBudget float64
}

func (a quotaAuth) Authenticate(_ context.Context, _ *http.Request) (*gateway.Identity, error) {
	return &gateway.Identity{
		Subject:    "test",
		KeyID:      "key-rl-1",
		OrgID:      "default",
		Role:       "admin",
		Perms:      gateway.RolePermissions["admin"],
		AuthMethod: "apikey",
		MaxBudget:  a.maxBudget,
	}, nil
}

func TestCacheHit(t *testing.T) {
	t.Parallel()
	mc, err := cache.NewMemory(100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	usage := &capturingRecorder{}
	h := newTestHandlerWith(func(d *Deps) {
		d.Cache = mc
		d.Usage = usage
	})

	// Low temperature makes it cacheable.
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"temperature":0.0}`

	// First request: cache miss, response served from provider.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// Allow otter async processing.
	time.Sleep(50 * time.Millisecond)

	// Second request: cache hit.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer gnd_test")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request: status = %d, want 200; body = %s", rec2.Code, rec2.Body.String())
	}
	if strings.TrimSpace(rec2.Body.String()) != strings.TrimSpace(rec.Body.String()) {
		t.Errorf("cache hit body mismatch:\n  miss: %s\n  hit:  %s", rec.Body.String(), rec2.Body.String())
	}

	// Verify cache hit was recorded.
	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.records) < 2 {
		t.Fatalf("expected >= 2 usage records, got %d", len(usage.records))
	}
	if !usage.records[1].Cached {
		t.Error("second request should be marked as cached")
	}
}

func TestStreamUsageRecording(t *testing.T) {
	t.Parallel()
	usage := &capturingRecorder{}
	h := newTestHandlerWith(func(d *Deps) {
		d.Usage = usage
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.records) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(usage.records))
	}
	if usage.records[0].Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", usage.records[0].Model)
	}
}

func TestEmbeddingsUsageRecording(t *testing.T) {
	t.Parallel()
	usage := &capturingRecorder{}
	h := newTestHandlerWith(func(d *Deps) {
		d.Usage = usage
	})

	body := `{"model":"text-embedding-3-small","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.records) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(usage.records))
	}
	if usage.records[0].PromptTokens != 3 {
		t.Errorf("prompt_tokens = %d, want 3", usage.records[0].PromptTokens)
	}
}

func TestEmbeddingsTPMDenied(t *testing.T) {
	t.Parallel()
	rl := ratelimit.NewRegistry()
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})

	h := New(Deps{
		Auth:        rateLimitAuth{rpm: 1000, tpm: 1},
		Proxy:       app.NewProxyService(reg, routerSvc),
		Providers:   reg,
		Router:      routerSvc,
		RateLimiter: rl,
	})

	body := `{"model":"text-embedding-3-small","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// With TPM=1, the default 100-token estimate should exceed.
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429; body = %s", rec.Code, rec.Body.String())
	}
}

func TestUsageRecordingWithQuota(t *testing.T) {
	t.Parallel()
	usage := &capturingRecorder{}
	qt := ratelimit.NewQuotaTracker()

	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	h := New(Deps{
		Auth:  quotaAuth{maxBudget: 100},
		Proxy: app.NewProxyService(reg, routerSvc),
		Providers: reg,
		Router:    routerSvc,
		Usage:     usage,
		Quota:     qt,
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// fakeProvider doesn't return Usage, so CostUSD should be 0.
	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(usage.records))
	}
}

func TestEstimateCost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		usage *gateway.Usage
		want  float64
	}{
		{"nil usage", nil, 0},
		{"100 tokens", &gateway.Usage{TotalTokens: 100}, 0.001},
		{"1000 tokens", &gateway.Usage{TotalTokens: 1000}, 0.01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := estimateCost("gpt-4o", tt.usage)
			if got != tt.want {
				t.Errorf("estimateCost() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestErrorStatus_AllBranches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want int
	}{
		{gateway.ErrUnauthorized, http.StatusUnauthorized},
		{gateway.ErrKeyExpired, http.StatusUnauthorized},
		{gateway.ErrForbidden, http.StatusForbidden},
		{gateway.ErrModelNotAllowed, http.StatusForbidden},
		{gateway.ErrKeyBlocked, http.StatusForbidden},
		{gateway.ErrNotFound, http.StatusNotFound},
		{gateway.ErrRateLimited, http.StatusTooManyRequests},
		{gateway.ErrBadRequest, http.StatusBadRequest},
		{errors.New("unknown"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			t.Parallel()
			if got := errorStatus(tt.err); got != tt.want {
				t.Errorf("errorStatus(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestStreamWithUsageChunk(t *testing.T) {
	t.Parallel()
	usage := &capturingRecorder{}
	rl := ratelimit.NewRegistry()

	// Provider that sends usage in stream.
	streamProv := &streamWithUsageProvider{}
	reg := provider.NewRegistry()
	reg.Register("fake", streamProv)
	routerSvc := app.NewRouterService(&fakeRouteStore{})

	h := New(Deps{
		Auth:        rateLimitAuth{rpm: 100, tpm: 100000},
		Proxy:       app.NewProxyService(reg, routerSvc),
		Providers:   reg,
		Router:      routerSvc,
		Usage:       usage,
		RateLimiter: rl,
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.records) != 1 {
		t.Fatalf("expected 1 usage record, got %d", len(usage.records))
	}
	if usage.records[0].TotalTokens != 42 {
		t.Errorf("total_tokens = %d, want 42", usage.records[0].TotalTokens)
	}
}

// streamWithUsageProvider sends usage in the stream chunks.
type streamWithUsageProvider struct{ fakeProvider }

func (streamWithUsageProvider) ChatCompletionStream(_ context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	ch := make(chan gateway.StreamChunk, 3)
	ch <- gateway.StreamChunk{Data: []byte(`{"id":"test","choices":[{"delta":{"content":"hi"}}]}`)}
	ch <- gateway.StreamChunk{Usage: &gateway.Usage{PromptTokens: 10, CompletionTokens: 32, TotalTokens: 42}}
	ch <- gateway.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func TestTokenCounterIntegration(t *testing.T) {
	t.Parallel()
	usage := &capturingRecorder{}
	rl := ratelimit.NewRegistry()

	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})
	routerSvc := app.NewRouterService(&fakeRouteStore{})
	h := New(Deps{
		Auth:         rateLimitAuth{rpm: 100, tpm: 100000},
		Proxy:        app.NewProxyService(reg, routerSvc),
		Providers:    reg,
		Router:       routerSvc,
		Usage:        usage,
		RateLimiter:  rl,
		TokenCounter: tokencount.NewCounter(),
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	// Verify TPM headers are set.
	if rec.Header().Get("X-Ratelimit-Limit-Tokens") == "" {
		t.Error("X-Ratelimit-Limit-Tokens should be set when TPM is configured")
	}
}
