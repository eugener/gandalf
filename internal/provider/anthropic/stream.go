package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider/sseutil"
)

// streamState tracks the state machine for Anthropic SSE streaming.
type streamState struct {
	id           string
	model        string
	inputTokens  int
	outputTokens int
	stopReason   string
}

// readStream reads Anthropic SSE events and emits OpenAI-format StreamChunks.
func readStream(ctx context.Context, body io.ReadCloser, ch chan<- gateway.StreamChunk) {
	defer close(ch)
	defer body.Close()

	var state streamState
	scanner := sseutil.NewScanner(body)

	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		event, data, ok := sseutil.ParseSSELine(line)
		if !ok {
			continue
		}
		if event != "" {
			currentEvent = event
			continue
		}
		if data == "" {
			continue
		}

		chunks := state.handleEvent(currentEvent, data)
		for _, c := range chunks {
			select {
			case ch <- c:
			case <-ctx.Done():
				ch <- gateway.StreamChunk{Err: ctx.Err()}
				return
			}
		}
		currentEvent = ""
	}
	if err := scanner.Err(); err != nil {
		ch <- gateway.StreamChunk{Err: fmt.Errorf("anthropic: read stream: %w", err)}
	}
}

// handleEvent processes a single Anthropic SSE event and returns zero or more
// OpenAI-format StreamChunks.
func (s *streamState) handleEvent(event, data string) []gateway.StreamChunk {
	switch event {
	case "message_start":
		return s.onMessageStart(data)
	case "content_block_delta":
		return s.onContentBlockDelta(data)
	case "message_delta":
		return s.onMessageDelta(data)
	case "message_stop":
		return s.onMessageStop()
	case "ping", "content_block_start", "content_block_stop":
		return nil
	default:
		return nil
	}
}

func (s *streamState) onMessageStart(data string) []gateway.StreamChunk {
	r := gjson.Parse(data)
	s.id = r.Get("message.id").String()
	s.model = r.Get("message.model").String()
	s.inputTokens = int(r.Get("message.usage.input_tokens").Int())

	// Emit initial role chunk.
	chunk := buildDeltaChunk(s.id, s.model, map[string]any{"role": "assistant"}, "")
	return []gateway.StreamChunk{{Data: chunk}}
}

func (s *streamState) onContentBlockDelta(data string) []gateway.StreamChunk {
	r := gjson.Parse(data)
	deltaType := r.Get("delta.type").String()

	switch deltaType {
	case "text_delta":
		text := r.Get("delta.text").String()
		chunk := buildDeltaChunk(s.id, s.model, map[string]any{"content": text}, "")
		return []gateway.StreamChunk{{Data: chunk}}

	case "input_json_delta":
		// Tool call argument delta.
		idx := int(r.Get("index").Int())
		partial := r.Get("delta.partial_json").String()
		chunk := buildToolCallDeltaChunk(s.id, s.model, idx, partial)
		return []gateway.StreamChunk{{Data: chunk}}
	}
	return nil
}

func (s *streamState) onMessageDelta(data string) []gateway.StreamChunk {
	r := gjson.Parse(data)
	s.outputTokens = int(r.Get("usage.output_tokens").Int())
	s.stopReason = r.Get("delta.stop_reason").String()
	return nil
}

func (s *streamState) onMessageStop() []gateway.StreamChunk {
	// Emit finish chunk with stop reason.
	finishReason := mapStopReason(s.stopReason)
	finishChunk := buildFinishChunk(s.id, s.model, finishReason)

	// Emit usage chunk.
	usage := &gateway.Usage{
		PromptTokens:     s.inputTokens,
		CompletionTokens: s.outputTokens,
		TotalTokens:      s.inputTokens + s.outputTokens,
	}
	usageChunk := buildUsageChunk(s.id, s.model, usage)

	return []gateway.StreamChunk{
		{Data: finishChunk},
		{Data: usageChunk, Usage: usage},
		{Done: true},
	}
}

// buildDeltaChunk builds an OpenAI-format streaming chunk JSON.
func buildDeltaChunk(id, model string, delta map[string]any, finishReason string) []byte {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": nilOrString(finishReason),
		}},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// buildToolCallDeltaChunk builds an OpenAI-format tool call delta chunk.
func buildToolCallDeltaChunk(id, model string, index int, argumentsDelta string) []byte {
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

// buildFinishChunk builds a chunk with finish_reason set.
func buildFinishChunk(id, model, finishReason string) []byte {
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

// buildUsageChunk builds a chunk with usage statistics.
func buildUsageChunk(id, model string, usage *gateway.Usage) []byte {
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

func nilOrString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
