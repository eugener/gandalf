package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/provider/sseutil"
)

// readStream reads Gemini SSE events and emits OpenAI-format StreamChunks.
// Gemini streaming has no "event:" field and no "[DONE]" sentinel -- it is
// EOF-terminated. Each "data:" line contains a full JSON response chunk.
// Usage is cumulative; we track the last seen values and emit them at the end.
func readStream(ctx context.Context, body io.ReadCloser, ch chan<- gateway.StreamChunk, model string) {
	defer close(ch)
	defer body.Close()

	scanner := sseutil.NewScanner(body)

	var lastUsage *gateway.Usage
	for scanner.Scan() {
		line := scanner.Text()
		_, data, ok := sseutil.ParseSSELine(line)
		if !ok {
			continue
		}

		r := gjson.Parse(data)

		// Extract text content delta.
		text := r.Get("candidates.0.content.parts.0.text").String()
		finishReason := mapStopReason(r.Get("candidates.0.finishReason").String())

		// Track cumulative usage.
		if u := r.Get("usageMetadata"); u.Exists() {
			lastUsage = &gateway.Usage{
				PromptTokens:     int(u.Get("promptTokenCount").Int()),
				CompletionTokens: int(u.Get("candidatesTokenCount").Int()),
				TotalTokens:      int(u.Get("totalTokenCount").Int()),
			}
		}

		if text != "" {
			chunk := buildDeltaChunk(model, map[string]any{"content": text}, finishReason)
			select {
			case ch <- gateway.StreamChunk{Data: chunk}:
			case <-ctx.Done():
				ch <- gateway.StreamChunk{Err: ctx.Err()}
				return
			}
		} else if finishReason != "" {
			chunk := buildDeltaChunk(model, map[string]any{}, finishReason)
			select {
			case ch <- gateway.StreamChunk{Data: chunk}:
			case <-ctx.Done():
				ch <- gateway.StreamChunk{Err: ctx.Err()}
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- gateway.StreamChunk{Err: fmt.Errorf("gemini: read stream: %w", err)}
		return
	}

	// Emit usage chunk at the end (Gemini provides cumulative usage).
	if lastUsage != nil {
		usageData := buildUsageChunk(model, lastUsage)
		ch <- gateway.StreamChunk{Data: usageData, Usage: lastUsage}
	}
	ch <- gateway.StreamChunk{Done: true}
}

// buildDeltaChunk builds an OpenAI-format streaming chunk JSON.
func buildDeltaChunk(model string, delta map[string]any, finishReason string) []byte {
	var fr any
	if finishReason != "" {
		fr = finishReason
	}
	chunk := map[string]any{
		"id":      "gemini-" + model,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": fr,
		}},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// buildUsageChunk builds a chunk with usage statistics.
func buildUsageChunk(model string, usage *gateway.Usage) []byte {
	chunk := map[string]any{
		"id":      "gemini-" + model,
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
