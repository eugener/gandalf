package anthropic

import (
	"context"
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
	chunk := sseutil.BuildDeltaChunk(s.id, s.model, map[string]any{"role": "assistant"}, "")
	return []gateway.StreamChunk{{Data: chunk}}
}

func (s *streamState) onContentBlockDelta(data string) []gateway.StreamChunk {
	r := gjson.Parse(data)
	deltaType := r.Get("delta.type").String()

	switch deltaType {
	case "text_delta":
		text := r.Get("delta.text").String()
		chunk := sseutil.BuildDeltaChunk(s.id, s.model, map[string]any{"content": text}, "")
		return []gateway.StreamChunk{{Data: chunk}}

	case "input_json_delta":
		// Tool call argument delta.
		idx := int(r.Get("index").Int())
		partial := r.Get("delta.partial_json").String()
		chunk := sseutil.BuildToolCallDeltaChunk(s.id, s.model, idx, partial)
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
	finishChunk := sseutil.BuildFinishChunk(s.id, s.model, finishReason)

	// Emit usage chunk.
	usage := &gateway.Usage{
		PromptTokens:     s.inputTokens,
		CompletionTokens: s.outputTokens,
		TotalTokens:      s.inputTokens + s.outputTokens,
	}
	usageChunk := sseutil.BuildUsageChunk(s.id, s.model, usage)

	return []gateway.StreamChunk{
		{Data: finishChunk},
		{Data: usageChunk, Usage: usage},
		{Done: true},
	}
}
