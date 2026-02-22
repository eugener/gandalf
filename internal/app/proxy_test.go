package app

import (
	"context"
	"errors"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
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

	ps := NewProxyService(reg, NewRouterService(store))
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

	ps := NewProxyService(reg, NewRouterService(store))
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

	ps := NewProxyService(reg, NewRouterService(store))
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

	ps := NewProxyService(reg, NewRouterService(store))
	_, err := ps.ChatCompletion(context.Background(), &gateway.ChatRequest{Model: "model-a"})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, gateway.ErrProviderError) {
		t.Errorf("expected ErrProviderError, got: %v", err)
	}
}
