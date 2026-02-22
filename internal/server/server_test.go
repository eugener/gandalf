package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
	"github.com/eugener/gandalf/internal/provider"
)

// fakeAuth always authenticates successfully.
type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, _ *http.Request) (*gateway.Identity, error) {
	return &gateway.Identity{
		Subject:    "test",
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
func (fakeProvider) ChatCompletionStream(context.Context, *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	return nil, gateway.ErrBadRequest
}
func (fakeProvider) Embeddings(context.Context, *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	return nil, gateway.ErrBadRequest
}
func (fakeProvider) ListModels(context.Context) ([]string, error) { return []string{"gpt-4o"}, nil }
func (fakeProvider) HealthCheck(context.Context) error             { return nil }

func newTestHandler() http.Handler {
	reg := provider.NewRegistry()
	reg.Register("fake", fakeProvider{})

	routerSvc := app.NewRouterService(&fakeRouteStore{})
	return New(Deps{
		Auth:  fakeAuth{},
		Proxy: app.NewProxyService(reg, routerSvc),
	})
}

// fakeRouteStore returns a route that maps to our fake provider.
type fakeRouteStore struct{}

func (fakeRouteStore) CreateRoute(context.Context, *gateway.Route) error { return nil }
func (fakeRouteStore) GetRouteByAlias(_ context.Context, alias string) (*gateway.Route, error) {
	return &gateway.Route{
		ID:         "r-1",
		ModelAlias: alias,
		Targets:    []byte(`[{"provider_id":"fake","model":"gpt-4o","priority":1}]`),
		Strategy:   "priority",
	}, nil
}
func (fakeRouteStore) ListRoutes(context.Context) ([]*gateway.Route, error) { return nil, nil }
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
