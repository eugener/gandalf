package server

import (
	"testing"

	gateway "github.com/eugener/gandalf/internal"
)

func TestCacheKey_Determinism(t *testing.T) {
	t.Parallel()
	temp := 0.1
	req := &gateway.ChatRequest{
		Model:       "gpt-4o",
		Messages:    []gateway.Message{{Role: "user", Content: []byte(`"hello"`)}},
		Temperature: &temp,
	}

	k1 := cacheKey("key1", req)
	k2 := cacheKey("key1", req)
	if k1 != k2 {
		t.Error("same request should produce same cache key")
	}
}

func TestCacheKey_DifferentInputs(t *testing.T) {
	t.Parallel()
	temp := 0.1
	r1 := &gateway.ChatRequest{
		Model:       "gpt-4o",
		Messages:    []gateway.Message{{Role: "user", Content: []byte(`"hello"`)}},
		Temperature: &temp,
	}
	r2 := &gateway.ChatRequest{
		Model:       "gpt-4o",
		Messages:    []gateway.Message{{Role: "user", Content: []byte(`"world"`)}},
		Temperature: &temp,
	}

	if cacheKey("key1", r1) == cacheKey("key1", r2) {
		t.Error("different messages should produce different keys")
	}
}

func TestCacheKey_WithAllFields(t *testing.T) {
	t.Parallel()
	temp := 0.1
	topP := 0.9
	maxTok := 100
	presP := 0.5
	freqP := 0.3
	seed := 42
	req := &gateway.ChatRequest{
		Model:            "gpt-4o",
		Messages:         []gateway.Message{{Role: "user", Content: []byte(`"hello"`), Name: "bob", ToolCallID: "tc1", ToolCalls: []byte(`[{"id":"1"}]`)}},
		Temperature:      &temp,
		TopP:             &topP,
		MaxTokens:        &maxTok,
		PresencePenalty:  &presP,
		FrequencyPenalty: &freqP,
		Seed:             &seed,
		Stop:             []byte(`["end"]`),
		Tools:            []byte(`[{"type":"function"}]`),
		ToolChoice:       []byte(`"auto"`),
		ResponseFormat:   []byte(`{"type":"json"}`),
	}

	k := cacheKey("key1", req)
	if k == "" {
		t.Error("cache key should not be empty")
	}
	if len(k) != 64 { // SHA-256 hex
		t.Errorf("cache key length = %d, want 64", len(k))
	}
}

func TestCacheKey_ModelDifference(t *testing.T) {
	t.Parallel()
	temp := 0.0
	r1 := &gateway.ChatRequest{
		Model:       "gpt-4o",
		Messages:    []gateway.Message{{Role: "user", Content: []byte(`"hello"`)}},
		Temperature: &temp,
	}
	r2 := &gateway.ChatRequest{
		Model:       "gpt-4o-mini",
		Messages:    []gateway.Message{{Role: "user", Content: []byte(`"hello"`)}},
		Temperature: &temp,
	}
	if cacheKey("key1", r1) == cacheKey("key1", r2) {
		t.Error("different models should produce different keys")
	}
}

func TestCacheKey_DifferentKeys(t *testing.T) {
	t.Parallel()
	temp := 0.0
	req := &gateway.ChatRequest{
		Model:       "gpt-4o",
		Messages:    []gateway.Message{{Role: "user", Content: []byte(`"hello"`)}},
		Temperature: &temp,
	}
	if cacheKey("key-a", req) == cacheKey("key-b", req) {
		t.Error("different API keys should produce different cache keys")
	}
}

func TestIsCacheable(t *testing.T) {
	t.Parallel()
	lowTemp := 0.1
	highTemp := 0.8
	seed := 42

	tests := []struct {
		name string
		req  *gateway.ChatRequest
		want bool
	}{
		{
			name: "low temperature",
			req:  &gateway.ChatRequest{Temperature: &lowTemp},
			want: true,
		},
		{
			name: "high temperature",
			req:  &gateway.ChatRequest{Temperature: &highTemp},
			want: false,
		},
		{
			name: "with seed",
			req:  &gateway.ChatRequest{Seed: &seed},
			want: true,
		},
		{
			name: "streaming",
			req:  &gateway.ChatRequest{Stream: true, Temperature: &lowTemp},
			want: false,
		},
		{
			name: "n > 1",
			req:  &gateway.ChatRequest{N: 2, Temperature: &lowTemp},
			want: false,
		},
		{
			name: "default temperature",
			req:  &gateway.ChatRequest{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isCacheable(tt.req); got != tt.want {
				t.Errorf("isCacheable() = %v, want %v", got, tt.want)
			}
		})
	}
}
