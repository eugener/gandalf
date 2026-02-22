// Package testutil provides configurable test fakes for gateway interfaces.
package testutil

import (
	"context"

	gateway "github.com/eugener/gandalf/internal"
)

// FakeProvider is a configurable gateway.Provider for testing.
type FakeProvider struct {
	ProviderName string
	ChatFn       func(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error)
	StreamFn     func(ctx context.Context, req *gateway.ChatRequest) (<-chan gateway.StreamChunk, error)
	EmbedFn      func(ctx context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error)
	ModelsFn     func(ctx context.Context) ([]string, error)
	HealthFn     func(ctx context.Context) error
}

// Name returns the configured provider name.
func (f *FakeProvider) Name() string { return f.ProviderName }

// ChatCompletion delegates to ChatFn or returns a default response.
func (f *FakeProvider) ChatCompletion(ctx context.Context, req *gateway.ChatRequest) (*gateway.ChatResponse, error) {
	if f.ChatFn != nil {
		return f.ChatFn(ctx, req)
	}
	return &gateway.ChatResponse{
		ID:      "chatcmpl-fake",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   req.Model,
		Choices: []gateway.Choice{{
			Index:        0,
			Message:      gateway.Message{Role: "assistant", Content: []byte(`"hello"`)},
			FinishReason: "stop",
		}},
	}, nil
}

// ChatCompletionStream delegates to StreamFn or returns an error.
func (f *FakeProvider) ChatCompletionStream(ctx context.Context, req *gateway.ChatRequest) (<-chan gateway.StreamChunk, error) {
	if f.StreamFn != nil {
		return f.StreamFn(ctx, req)
	}
	return nil, gateway.ErrProviderError
}

// Embeddings delegates to EmbedFn or returns an error.
func (f *FakeProvider) Embeddings(ctx context.Context, req *gateway.EmbeddingRequest) (*gateway.EmbeddingResponse, error) {
	if f.EmbedFn != nil {
		return f.EmbedFn(ctx, req)
	}
	return nil, gateway.ErrProviderError
}

// ListModels delegates to ModelsFn or returns a default list.
func (f *FakeProvider) ListModels(ctx context.Context) ([]string, error) {
	if f.ModelsFn != nil {
		return f.ModelsFn(ctx)
	}
	return []string{"fake-model"}, nil
}

// HealthCheck delegates to HealthFn or returns nil.
func (f *FakeProvider) HealthCheck(ctx context.Context) error {
	if f.HealthFn != nil {
		return f.HealthFn(ctx)
	}
	return nil
}

// FakeStreamChan returns a channel pre-loaded with the given chunks, followed
// by a Done sentinel. The channel is closed after all chunks are sent.
func FakeStreamChan(chunks ...gateway.StreamChunk) <-chan gateway.StreamChunk {
	ch := make(chan gateway.StreamChunk, len(chunks)+1)
	for _, c := range chunks {
		ch <- c
	}
	ch <- gateway.StreamChunk{Done: true}
	close(ch)
	return ch
}
