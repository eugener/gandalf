package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// Cache is the interface for response caching used by the server.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration)
	Delete(ctx context.Context, key string)
	Purge(ctx context.Context)
}

// isCacheable returns true if the request is eligible for caching.
// Only non-streaming requests with low/zero temperature or a seed are cacheable.
func isCacheable(req *gateway.ChatRequest) bool {
	if req.Stream {
		return false
	}
	if req.N > 1 {
		return false
	}
	if req.Seed != nil {
		return true
	}
	if req.Temperature != nil && *req.Temperature <= 0.3 {
		return true
	}
	// Default temperature (nil) is usually 1.0, not cacheable.
	return false
}

// cacheKey produces a deterministic SHA-256 hash for a ChatRequest,
// scoped to the caller's API key to prevent cross-user response leakage.
func cacheKey(keyID string, req *gateway.ChatRequest) string {
	// Build a normalized map for stable JSON output.
	m := map[string]any{
		"key_id":   keyID,
		"model":    req.Model,
		"messages": normalizeMessages(req.Messages),
	}
	if req.Temperature != nil {
		m["temperature"] = roundFloat(*req.Temperature)
	}
	if req.TopP != nil {
		m["top_p"] = roundFloat(*req.TopP)
	}
	if req.MaxTokens != nil {
		m["max_tokens"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		m["stop"] = json.RawMessage(req.Stop)
	}
	if req.PresencePenalty != nil {
		m["presence_penalty"] = roundFloat(*req.PresencePenalty)
	}
	if req.FrequencyPenalty != nil {
		m["frequency_penalty"] = roundFloat(*req.FrequencyPenalty)
	}
	if req.Seed != nil {
		m["seed"] = *req.Seed
	}
	if len(req.Tools) > 0 {
		m["tools"] = json.RawMessage(req.Tools)
	}
	if len(req.ToolChoice) > 0 {
		m["tool_choice"] = json.RawMessage(req.ToolChoice)
	}
	if len(req.ResponseFormat) > 0 {
		m["response_format"] = json.RawMessage(req.ResponseFormat)
	}

	// Stable key order via sorted keys.
	data := stableJSON(m)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// stableMessage is a struct-based representation of a chat message for cache
// key computation. Struct fields marshal in declaration order, avoiding the
// non-deterministic map iteration that caused cache key instability.
type stableMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

func normalizeMessages(msgs []gateway.Message) []stableMessage {
	out := make([]stableMessage, len(msgs))
	for i, m := range msgs {
		out[i] = stableMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		}
	}
	return out
}

func stableJSON(m map[string]any) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make([]struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}, len(keys))
	for i, k := range keys {
		ordered[i].Key = k
		ordered[i].Value = m[k]
	}

	data, _ := json.Marshal(ordered)
	return data
}

func roundFloat(f float64) float64 {
	return math.Round(f*10000) / 10000
}
