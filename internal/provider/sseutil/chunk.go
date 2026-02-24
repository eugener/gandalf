package sseutil

import (
	"encoding/json"

	gateway "github.com/eugener/gandalf/internal"
)

// BuildDeltaChunk builds an OpenAI-format streaming chunk JSON.
func BuildDeltaChunk(id, model string, delta map[string]any, finishReason string) []byte {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": NilOrString(finishReason),
		}},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// BuildToolCallDeltaChunk builds an OpenAI-format tool call delta chunk.
func BuildToolCallDeltaChunk(id, model string, index int, argumentsDelta string) []byte {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index": index,
					"function": map[string]any{
						"arguments": argumentsDelta,
					},
				}},
			},
			"finish_reason": nil,
		}},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// BuildFinishChunk builds a chunk with finish_reason set.
func BuildFinishChunk(id, model, finishReason string) []byte {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": finishReason,
		}},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// BuildUsageChunk builds a chunk with usage statistics.
func BuildUsageChunk(id, model string, usage *gateway.Usage) []byte {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{},
		"usage": map[string]any{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
		},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// NilOrString returns nil if s is empty, otherwise s.
func NilOrString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
