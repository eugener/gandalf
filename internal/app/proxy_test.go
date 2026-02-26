package app

import (
	"context"
	"errors"
	"testing"
	"time"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/circuitbreaker"
	"github.com/eugener/gandalf/internal/provider"
	"github.com/eugener/gandalf/internal/testutil"
)

func TestChatCompletion_PrimarySucceeds(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("openai", &testutil.FakeProvider{ProviderName: "openai"})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "gpt-4o",
		Targets:    []byte(`[{"provider_id":"openai","model":"gpt-4o","priority":1}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	resp, err := ps.ChatCompletion(context.Background(), &gateway.ChatRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.ID != "chatcmpl-fake" {
		t.Errorf("id = %q, want chatcmpl-fake", resp.ID)
	}
}

func TestChatCompletion_FailoverToSecondary(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("primary", &testutil.FakeProvider{
		ProviderName: "primary",
		ChatFn: func(context.Context, *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return nil, errors.New("primary down")
		},
	})
	reg.Register("secondary", &testutil.FakeProvider{
		ProviderName: "secondary",
		ChatFn: func(_ context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return &gateway.ChatResponse{ID: "from-secondary", Model: req.Model}, nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"primary","model":"model-a","priority":1},{"provider_id":"secondary","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	resp, err := ps.ChatCompletion(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.ID != "from-secondary" {
		t.Errorf("id = %q, want from-secondary", resp.ID)
	}
}

func TestChatCompletion_ClientErrorNoFailover(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("primary", &testutil.FakeProvider{
		ProviderName: "primary",
		ChatFn: func(context.Context, *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return nil, gateway.ErrBadRequest
		},
	})
	reg.Register("secondary", &testutil.FakeProvider{
		ProviderName: "secondary",
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"primary","model":"model-a","priority":1},{"provider_id":"secondary","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	_, err := ps.ChatCompletion(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if !errors.Is(err, gateway.ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got: %v", err)
	}
}

func TestChatCompletion_AllFail(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("p1", &testutil.FakeProvider{
		ProviderName: "p1",
		ChatFn: func(context.Context, *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return nil, errors.New("p1 down")
		},
	})
	reg.Register("p2", &testutil.FakeProvider{
		ProviderName: "p2",
		ChatFn: func(context.Context, *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return nil, errors.New("p2 down")
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"p1","model":"model-a","priority":1},{"provider_id":"p2","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	_, err := ps.ChatCompletion(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, gateway.ErrProviderError) {
		t.Errorf("expected ErrProviderError, got: %v", err)
	}
}

// --- ChatCompletionStream ---

func TestChatCompletionStream_PrimarySucceeds(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("openai", &testutil.FakeProvider{
		ProviderName: "openai",
		StreamFn: func(_ context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return testutil.FakeStreamChan(gateway.StreamChunk{Data: []byte("hello")}), nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "gpt-4o",
		Targets:    []byte(`[{"provider_id":"openai","model":"gpt-4o","priority":1}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	ch, err := ps.ChatCompletionStream(context.Background(), &gateway.ChatRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	first := <-ch
	if string(first.Data) != "hello" {
		t.Errorf("data = %q, want hello", first.Data)
	}
}

func TestChatCompletionStream_FailoverToSecondary(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("primary", &testutil.FakeProvider{
		ProviderName: "primary",
		StreamFn: func(context.Context, *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return nil, errors.New("primary stream down")
		},
	})
	reg.Register("secondary", &testutil.FakeProvider{
		ProviderName: "secondary",
		StreamFn: func(_ context.Context, _ *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return testutil.FakeStreamChan(gateway.StreamChunk{Data: []byte("fallback")}), nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"primary","model":"model-a","priority":1},{"provider_id":"secondary","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	ch, err := ps.ChatCompletionStream(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	first := <-ch
	if string(first.Data) != "fallback" {
		t.Errorf("data = %q, want fallback", first.Data)
	}
}

func TestChatCompletionStream_ClientErrorNoFailover(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("primary", &testutil.FakeProvider{
		ProviderName: "primary",
		StreamFn: func(context.Context, *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return nil, gateway.ErrBadRequest
		},
	})
	reg.Register("secondary", &testutil.FakeProvider{ProviderName: "secondary"})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"primary","model":"model-a","priority":1},{"provider_id":"secondary","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	_, err := ps.ChatCompletionStream(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if !errors.Is(err, gateway.ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got: %v", err)
	}
}

func TestChatCompletionStream_AllFail(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("p1", &testutil.FakeProvider{
		ProviderName: "p1",
		StreamFn: func(context.Context, *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return nil, errors.New("p1 stream down")
		},
	})
	reg.Register("p2", &testutil.FakeProvider{
		ProviderName: "p2",
		StreamFn: func(context.Context, *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
			return nil, errors.New("p2 stream down")
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"p1","model":"model-a","priority":1},{"provider_id":"p2","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	_, err := ps.ChatCompletionStream(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, gateway.ErrProviderError) {
		t.Errorf("expected ErrProviderError, got: %v", err)
	}
}

// --- Embeddings ---

func TestEmbeddings_PrimarySucceeds(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("openai", &testutil.FakeProvider{
		ProviderName: "openai",
		EmbedFn: func(_ context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
			return &gateway.EmbeddingResponse{Object: "list", Model: req.Model}, nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "text-embed",
		Targets:    []byte(`[{"provider_id":"openai","model":"text-embed","priority":1}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	resp, err := ps.Embeddings(context.Background(), &gateway.EmbeddingRequest{Model: "text-embed"})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
}

func TestEmbeddings_FailoverToSecondary(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("primary", &testutil.FakeProvider{
		ProviderName: "primary",
		EmbedFn: func(context.Context, *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
			return nil, errors.New("primary embed down")
		},
	})
	reg.Register("secondary", &testutil.FakeProvider{
		ProviderName: "secondary",
		EmbedFn: func(_ context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
			return &gateway.EmbeddingResponse{Object: "list", Model: req.Model}, nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "text-embed",
		Targets:    []byte(`[{"provider_id":"primary","model":"text-embed","priority":1},{"provider_id":"secondary","model":"text-embed","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	resp, err := ps.Embeddings(context.Background(), &gateway.EmbeddingRequest{Model: "text-embed"})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
}

func TestEmbeddings_ClientErrorNoFailover(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("primary", &testutil.FakeProvider{
		ProviderName: "primary",
		EmbedFn: func(context.Context, *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
			return nil, gateway.ErrBadRequest
		},
	})
	reg.Register("secondary", &testutil.FakeProvider{ProviderName: "secondary"})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "text-embed",
		Targets:    []byte(`[{"provider_id":"primary","model":"text-embed","priority":1},{"provider_id":"secondary","model":"text-embed","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	_, err := ps.Embeddings(context.Background(), &gateway.EmbeddingRequest{Model: "text-embed"})
	if !errors.Is(err, gateway.ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got: %v", err)
	}
}

func TestEmbeddings_AllFail(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("p1", &testutil.FakeProvider{
		ProviderName: "p1",
		EmbedFn: func(context.Context, *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
			return nil, errors.New("p1 embed down")
		},
	})
	reg.Register("p2", &testutil.FakeProvider{
		ProviderName: "p2",
		EmbedFn: func(context.Context, *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
			return nil, errors.New("p2 embed down")
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "text-embed",
		Targets:    []byte(`[{"provider_id":"p1","model":"text-embed","priority":1},{"provider_id":"p2","model":"text-embed","priority":2}]`),
		Strategy:   "priority",
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, nil)
	_, err := ps.Embeddings(context.Background(), &gateway.EmbeddingRequest{Model: "text-embed"})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, gateway.ErrProviderError) {
		t.Errorf("expected ErrProviderError, got: %v", err)
	}
}

// --- ListModels ---

func TestListModels_AggregatesAllProviders(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("p1", &testutil.FakeProvider{
		ProviderName: "p1",
		ModelsFn: func(context.Context) ([]string, error) {
			return []string{"p1-model-a", "p1-model-b"}, nil
		},
	})
	reg.Register("p2", &testutil.FakeProvider{
		ProviderName: "p2",
		ModelsFn: func(context.Context) ([]string, error) {
			return []string{"p2-model-x"}, nil
		},
	})

	ps := NewProxyService(reg, NewRouterService(testutil.NewFakeStore()), nil, nil)
	models, err := ps.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	want := map[string]bool{"p1-model-a": true, "p1-model-b": true, "p2-model-x": true}
	if len(models) != len(want) {
		t.Fatalf("got %d models, want %d: %v", len(models), len(want), models)
	}
	for _, m := range models {
		if !want[m] {
			t.Errorf("unexpected model %q", m)
		}
	}
}

func TestListModels_SkipsFailingProvider(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("good", &testutil.FakeProvider{
		ProviderName: "good",
		ModelsFn: func(context.Context) ([]string, error) {
			return []string{"good-model"}, nil
		},
	})
	reg.Register("bad", &testutil.FakeProvider{
		ProviderName: "bad",
		ModelsFn: func(context.Context) ([]string, error) {
			return nil, errors.New("bad provider down")
		},
	})

	ps := NewProxyService(reg, NewRouterService(testutil.NewFakeStore()), nil, nil)
	models, err := ps.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0] != "good-model" {
		t.Errorf("models = %v, want [good-model]", models)
	}
}

// --- Circuit Breaker Integration ---

func TestChatCompletion_CircuitBreakerSkipsOpenProvider(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("bad", &testutil.FakeProvider{
		ProviderName: "bad",
		ChatFn: func(context.Context, *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return nil, errors.New("should not be called")
		},
	})
	reg.Register("good", &testutil.FakeProvider{
		ProviderName: "good",
		ChatFn: func(_ context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return &gateway.ChatResponse{ID: "from-good", Model: req.Model}, nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"bad","model":"model-a","priority":1},{"provider_id":"good","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	cbReg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		ErrorThreshold: 0.30,
		MinSamples:     5,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	})

	// Trip the breaker for "bad" provider.
	cb := cbReg.GetOrCreate("bad")
	for range 10 {
		cb.RecordError(1.0)
	}

	ps := NewProxyService(reg, NewRouterService(store), nil, cbReg)
	resp, err := ps.ChatCompletion(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.ID != "from-good" {
		t.Errorf("id = %q, want from-good (should skip open breaker)", resp.ID)
	}
}

func TestChatCompletion_CircuitBreakerRecordsErrors(t *testing.T) {
	t.Parallel()

	reg := provider.NewRegistry()
	reg.Register("flaky", &testutil.FakeProvider{
		ProviderName: "flaky",
		ChatFn: func(context.Context, *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return nil, errors.New("server error")
		},
	})
	reg.Register("backup", &testutil.FakeProvider{
		ProviderName: "backup",
		ChatFn: func(_ context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
			return &gateway.ChatResponse{ID: "from-backup", Model: req.Model}, nil
		},
	})

	store := testutil.NewFakeStore()
	store.AddRoute(&gateway.Route{
		ID:         "r-1",
		ModelAlias: "model-a",
		Targets:    []byte(`[{"provider_id":"flaky","model":"model-a","priority":1},{"provider_id":"backup","model":"model-a","priority":2}]`),
		Strategy:   "priority",
	})

	cbReg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		ErrorThreshold: 0.30,
		MinSamples:     5,
		WindowSeconds:  60,
		OpenTimeout:    30 * time.Second,
	})

	ps := NewProxyService(reg, NewRouterService(store), nil, cbReg)

	// Make enough requests to trip the breaker for "flaky".
	for range 6 {
		ps.ChatCompletion(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	}

	// Breaker for "flaky" should now be open.
	cb := cbReg.Get("flaky")
	if cb == nil {
		t.Fatal("expected breaker for flaky provider")
	}
	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("state = %v, want open", cb.State())
	}
}
