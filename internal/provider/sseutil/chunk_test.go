package sseutil

import (
	"encoding/json"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
)

func TestBuildDeltaChunk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		id, model    string
		delta        map[string]any
		finishReason string
		wantFinish   any // nil or string
	}{
		{
			name:       "content delta without finish",
			id:         "chatcmpl-1",
			model:      "gpt-4o",
			delta:      map[string]any{"content": "Hello"},
			wantFinish: nil,
		},
		{
			name:         "content delta with finish",
			id:           "chatcmpl-2",
			model:        "gpt-4o",
			delta:        map[string]any{"content": " world"},
			finishReason: "stop",
			wantFinish:   "stop",
		},
		{
			name:       "empty delta",
			id:         "chatcmpl-3",
			model:      "gpt-4o",
			delta:      map[string]any{},
			wantFinish: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := BuildDeltaChunk(tt.id, tt.model, tt.delta, tt.finishReason)

			var parsed map[string]any
			if err := json.Unmarshal(b, &parsed); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if parsed["id"] != tt.id {
				t.Errorf("id = %v, want %v", parsed["id"], tt.id)
			}
			if parsed["object"] != "chat.completion.chunk" {
				t.Errorf("object = %v", parsed["object"])
			}
			if parsed["model"] != tt.model {
				t.Errorf("model = %v, want %v", parsed["model"], tt.model)
			}

			choices := parsed["choices"].([]any)
			if len(choices) != 1 {
				t.Fatalf("choices len = %d, want 1", len(choices))
			}
			choice := choices[0].(map[string]any)
			if choice["finish_reason"] != tt.wantFinish {
				t.Errorf("finish_reason = %v, want %v", choice["finish_reason"], tt.wantFinish)
			}
		})
	}
}

func TestBuildToolCallDeltaChunk(t *testing.T) {
	t.Parallel()

	b := BuildToolCallDeltaChunk("chatcmpl-1", "gpt-4o", 0, `{"name":"foo"}`)

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	choices := parsed["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != nil {
		t.Errorf("finish_reason = %v, want nil", choice["finish_reason"])
	}

	delta := choice["delta"].(map[string]any)
	toolCalls := delta["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(toolCalls))
	}
	tc := toolCalls[0].(map[string]any)
	if tc["index"] != float64(0) {
		t.Errorf("index = %v, want 0", tc["index"])
	}
	fn := tc["function"].(map[string]any)
	if fn["arguments"] != `{"name":"foo"}` {
		t.Errorf("arguments = %v", fn["arguments"])
	}
}

func TestBuildFinishChunk(t *testing.T) {
	t.Parallel()

	b := BuildFinishChunk("chatcmpl-1", "gpt-4o", "stop")

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	choices := parsed["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}
	// Delta should be empty.
	delta := choice["delta"].(map[string]any)
	if len(delta) != 0 {
		t.Errorf("delta should be empty, got %v", delta)
	}
}

func TestBuildUsageChunk(t *testing.T) {
	t.Parallel()

	usage := &gateway.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	b := BuildUsageChunk("chatcmpl-1", "gpt-4o", usage)

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	choices := parsed["choices"].([]any)
	if len(choices) != 0 {
		t.Errorf("choices should be empty, got %d", len(choices))
	}

	u := parsed["usage"].(map[string]any)
	if u["prompt_tokens"] != float64(10) {
		t.Errorf("prompt_tokens = %v, want 10", u["prompt_tokens"])
	}
	if u["completion_tokens"] != float64(5) {
		t.Errorf("completion_tokens = %v, want 5", u["completion_tokens"])
	}
	if u["total_tokens"] != float64(15) {
		t.Errorf("total_tokens = %v, want 15", u["total_tokens"])
	}
}

func TestNilOrString(t *testing.T) {
	t.Parallel()

	if v := NilOrString(""); v != nil {
		t.Errorf("NilOrString(\"\") = %v, want nil", v)
	}
	if v := NilOrString("stop"); v != "stop" {
		t.Errorf("NilOrString(\"stop\") = %v, want \"stop\"", v)
	}
}
