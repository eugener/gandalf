package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/provider"
)

// fakeNativeProvider implements both gateway.Provider and gateway.NativeProxy.
type fakeNativeProvider struct {
	fakeProvider
	name         string
	providerType string
	lastPath     string
	lastBody     string
	lastHeaders  http.Header
}

func (f *fakeNativeProvider) Name() string { return f.name }
func (f *fakeNativeProvider) Type() string {
	if f.providerType != "" {
		return f.providerType
	}
	return f.name
}

func (f *fakeNativeProvider) ProxyRequest(_ context.Context, w http.ResponseWriter, r *http.Request, path string) error {
	f.lastPath = path
	body, _ := io.ReadAll(r.Body)
	f.lastBody = string(body)
	f.lastHeaders = r.Header.Clone()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"proxied":true,"path":"` + path + `"}`))
	return nil
}

// fakeNativeRouteStore maps model aliases to specific provider IDs.
type fakeNativeRouteStore struct {
	routes map[string]string // model -> provider_id
}

func (f *fakeNativeRouteStore) CreateRoute(context.Context, *gateway.Route) error { return nil }
func (f *fakeNativeRouteStore) GetRoute(context.Context, string) (*gateway.Route, error) {
	return nil, gateway.ErrNotFound
}
func (f *fakeNativeRouteStore) GetRouteByAlias(_ context.Context, alias string) (*gateway.Route, error) {
	pid, ok := f.routes[alias]
	if !ok {
		return nil, gateway.ErrNotFound
	}
	return &gateway.Route{
		ID:         "r-native",
		ModelAlias: alias,
		Targets:    []byte(`[{"provider_id":"` + pid + `","model":"` + alias + `","priority":1}]`),
		Strategy:   "priority",
	}, nil
}
func (f *fakeNativeRouteStore) ListRoutes(context.Context) ([]*gateway.Route, error) { return nil, nil }
func (f *fakeNativeRouteStore) CountRoutes(context.Context) (int, error)             { return 0, nil }
func (f *fakeNativeRouteStore) UpdateRoute(context.Context, *gateway.Route) error    { return nil }
func (f *fakeNativeRouteStore) DeleteRoute(context.Context, string) error            { return nil }

func newNativeTestHandler(providers map[string]*fakeNativeProvider, routes map[string]string) http.Handler {
	reg := provider.NewRegistry()
	for name, p := range providers {
		reg.Register(name, p)
	}
	routerSvc := app.NewRouterService(&fakeNativeRouteStore{routes: routes})
	return New(Deps{
		Auth:      fakeAuth{},
		Proxy:     app.NewProxyService(reg, routerSvc),
		Providers: reg,
		Router:    routerSvc,
	})
}

func TestNativeAnthropicMessages(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "anthropic"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"anthropic": fp},
		map[string]string{"claude-sonnet-4-6": "anthropic"},
	)

	body := `{"model":"claude-sonnet-4-6","max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/messages" {
		t.Errorf("path = %q, want /messages", fp.lastPath)
	}
	if !strings.Contains(fp.lastBody, "claude-sonnet-4-6") {
		t.Errorf("body not forwarded, got: %s", fp.lastBody)
	}
	if !strings.Contains(rec.Body.String(), `"proxied":true`) {
		t.Errorf("response not proxied, got: %s", rec.Body.String())
	}
}

func TestNativeAnthropicAuthNormalization(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "anthropic"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"anthropic": fp},
		map[string]string{"claude-sonnet-4-6": "anthropic"},
	)

	// Send with x-api-key, should be normalized to Authorization: Bearer
	body := `{"model":"claude-sonnet-4-6","max_tokens":100,"messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestNativeAnthropicAuthorizationNotOverridden(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "anthropic"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"anthropic": fp},
		map[string]string{"claude-sonnet-4-6": "anthropic"},
	)

	// Send with both Authorization and x-api-key; Authorization should win
	body := `{"model":"claude-sonnet-4-6","max_tokens":100,"messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_auth_key")
	req.Header.Set("X-Api-Key", "gnd_other_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestNativeGeminiGenerateContent(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "gemini"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"gemini": fp},
		map[string]string{"gemini-2.0-flash": "gemini"},
	)

	body := `{"contents":[{"parts":[{"text":"hello"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/models/gemini-2.0-flash:generateContent" {
		t.Errorf("path = %q, want /models/gemini-2.0-flash:generateContent", fp.lastPath)
	}
}

func TestNativeGeminiStreamGenerateContent(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "gemini"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"gemini": fp},
		map[string]string{"gemini-2.0-flash": "gemini"},
	)

	body := `{"contents":[{"parts":[{"text":"hello"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:streamGenerateContent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/models/gemini-2.0-flash:streamGenerateContent" {
		t.Errorf("path = %q, want /models/gemini-2.0-flash:streamGenerateContent", fp.lastPath)
	}
}

func TestNativeGeminiListModels(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "gemini"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"gemini": fp},
		map[string]string{},
	)

	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	req.Header.Set("X-Goog-Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/models" {
		t.Errorf("path = %q, want /models", fp.lastPath)
	}
}

func TestNativeAzureOpenAIChatCompletions(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "openai"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"openai": fp},
		map[string]string{"gpt-4o": "openai"},
	)

	body := `{"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/openai/deployments/gpt-4o/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", fp.lastPath)
	}
}

func TestNativeAzureOpenAIEmbeddings(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "openai"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"openai": fp},
		map[string]string{"text-embedding-3-small": "openai"},
	)

	body := `{"input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/openai/deployments/text-embedding-3-small/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/embeddings" {
		t.Errorf("path = %q, want /embeddings", fp.lastPath)
	}
}

func TestNativeOllamaChat(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "ollama"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"ollama": fp},
		map[string]string{"llama3": "ollama"},
	)

	body := `{"model":"llama3","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/chat" {
		t.Errorf("path = %q, want /chat", fp.lastPath)
	}
}

func TestNativeOllamaEmbed(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "ollama"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"ollama": fp},
		map[string]string{"nomic-embed-text": "ollama"},
	)

	body := `{"model":"nomic-embed-text","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/embed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/embed" {
		t.Errorf("path = %q, want /embed", fp.lastPath)
	}
}

func TestNativeOllamaTags(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "ollama"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"ollama": fp},
		map[string]string{},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req.Header.Set("Authorization", "Bearer gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if fp.lastPath != "/tags" {
		t.Errorf("path = %q, want /tags", fp.lastPath)
	}
}

func TestNativeNoProviderAvailable(t *testing.T) {
	t.Parallel()

	// Register an anthropic provider but request a model routed to "openai"
	fp := &fakeNativeProvider{name: "anthropic"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"anthropic": fp},
		map[string]string{"unknown-model": "openai"}, // route points to openai but we only have anthropic
	)

	body := `{"model":"unknown-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestNativeMissingModel(t *testing.T) {
	t.Parallel()

	fp := &fakeNativeProvider{name: "anthropic"}
	h := newNativeTestHandler(
		map[string]*fakeNativeProvider{"anthropic": fp},
		map[string]string{},
	)

	body := `{"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "gnd_test_key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestNormalizeAuthMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		value      string
		existAuth  string
		wantAuth   string
	}{
		{
			name:     "normalizes x-api-key",
			header:   "X-Api-Key",
			value:    "sk-test",
			wantAuth: "Bearer sk-test",
		},
		{
			name:      "does not override existing Authorization",
			header:    "X-Api-Key",
			value:     "sk-other",
			existAuth: "Bearer sk-existing",
			wantAuth:  "Bearer sk-existing",
		},
		{
			name:     "normalizes x-goog-api-key",
			header:   "X-Goog-Api-Key",
			value:    "goog-test",
			wantAuth: "Bearer goog-test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotAuth string
			inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
			})

			mw := normalizeAuth(tt.header)(inner)
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Header.Set(tt.header, tt.value)
			if tt.existAuth != "" {
				req.Header.Set("Authorization", tt.existAuth)
			}
			mw.ServeHTTP(httptest.NewRecorder(), req)

			if gotAuth != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", gotAuth, tt.wantAuth)
			}
		})
	}
}
