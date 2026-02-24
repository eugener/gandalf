package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
)

// fakeProvider is a minimal gateway.Provider for registry tests.
type fakeProvider struct {
	name, typ string
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Type() string { return f.typ }

func (f *fakeProvider) ChatCompletion(_ context.Context, _ *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	return nil, nil
}
func (f *fakeProvider) ChatCompletionStream(_ context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	return nil, nil
}
func (f *fakeProvider) Embeddings(_ context.Context, _ *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	return nil, nil
}
func (f *fakeProvider) ListModels(_ context.Context) ([]string, error) { return nil, nil }
func (f *fakeProvider) HealthCheck(_ context.Context) error            { return nil }

func TestRegistryRegisterAndGet(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	p := &fakeProvider{name: "openai-us", typ: "openai"}
	reg.Register("openai-us", p)

	got, err := reg.Get("openai-us")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name() != "openai-us" {
		t.Errorf("Name() = %q, want openai-us", got.Name())
	}

	_, err = reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestRegistryGetByType(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register("openai-us", &fakeProvider{name: "openai-us", typ: "openai"})
	reg.Register("gemini-1", &fakeProvider{name: "gemini-1", typ: "gemini"})

	got, err := reg.GetByType("gemini")
	if err != nil {
		t.Fatalf("GetByType: %v", err)
	}
	if got.Type() != "gemini" {
		t.Errorf("Type() = %q, want gemini", got.Type())
	}

	_, err = reg.GetByType("anthropic")
	if err == nil {
		t.Fatal("expected error for nonexistent type")
	}
}

func TestRegistryList(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register("beta", &fakeProvider{name: "beta", typ: "openai"})
	reg.Register("alpha", &fakeProvider{name: "alpha", typ: "gemini"})
	reg.Register("gamma", &fakeProvider{name: "gamma", typ: "ollama"})

	names := reg.List()
	if len(names) != 3 {
		t.Fatalf("got %d names, want 3", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" || names[2] != "gamma" {
		t.Errorf("names = %v, want [alpha beta gamma]", names)
	}
}

func TestRegistryOverwrite(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register("p1", &fakeProvider{name: "p1", typ: "openai"})
	reg.Register("p1", &fakeProvider{name: "p1", typ: "gemini"})

	got, err := reg.Get("p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type() != "gemini" {
		t.Errorf("Type() = %q, want gemini (overwritten)", got.Type())
	}
	if len(reg.List()) != 1 {
		t.Errorf("list len = %d, want 1", len(reg.List()))
	}
}

func TestAPIError(t *testing.T) {
	t.Parallel()

	err := &APIError{Provider: "openai", StatusCode: 429, Body: "rate limited"}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("Error() = %q, want to contain provider", err.Error())
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("Error() = %q, want to contain status", err.Error())
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("Error() = %q, want to contain body", err.Error())
	}
	if err.HTTPStatus() != http.StatusTooManyRequests {
		t.Errorf("HTTPStatus() = %d, want %d", err.HTTPStatus(), http.StatusTooManyRequests)
	}
}

func TestParseAPIError(t *testing.T) {
	t.Parallel()

	body := `{"error":{"message":"model not found"}}`
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	err := ParseAPIError("gemini", resp)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.HTTPStatus() != 404 {
		t.Errorf("HTTPStatus() = %d, want 404", apiErr.HTTPStatus())
	}
	if !strings.Contains(apiErr.Error(), "model not found") {
		t.Errorf("Error() = %q, want body content", apiErr.Error())
	}
}
