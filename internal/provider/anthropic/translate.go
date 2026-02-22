// Package anthropic implements the gateway.Provider adapter for the Anthropic API.
package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
)

// anthropicRequest is the Anthropic Messages API request body.
type anthropicRequest struct {
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens"`
	Messages    []anthropicMsg    `json:"messages"`
	System      json.RawMessage   `json:"system,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	TopP        *float64          `json:"top_p,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Tools       json.RawMessage   `json:"tools,omitempty"`
	StopSeqs    json.RawMessage   `json:"stop_sequences,omitempty"`
}

type anthropicMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// translateRequest converts an OpenAI-format ChatRequest to an Anthropic Messages API request.
func translateRequest(req *gateway.ChatRequest) (*anthropicRequest, error) {
	out := &anthropicRequest{
		Model:       req.Model,
		MaxTokens:   4096, // Anthropic requires max_tokens
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Tools:       req.Tools,
		StopSeqs:    req.Stop,
	}
	if req.MaxTokens != nil {
		out.MaxTokens = *req.MaxTokens
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			out.System = m.Content
		case "user", "assistant":
			out.Messages = append(out.Messages, anthropicMsg{
				Role:    m.Role,
				Content: m.Content,
			})
		case "tool":
			// Tool results map to user role in Anthropic's format.
			toolResult := fmt.Sprintf(`[{"type":"tool_result","tool_use_id":%q,"content":%s}]`,
				m.ToolCallID, string(m.Content))
			out.Messages = append(out.Messages, anthropicMsg{
				Role:    "user",
				Content: json.RawMessage(toolResult),
			})
		}
	}

	return out, nil
}

// translateResponse converts an Anthropic Messages API JSON response to an
// OpenAI-format ChatResponse.
func translateResponse(data []byte) (*gateway.ChatResponse, error) {
	result := gjson.ParseBytes(data)

	id := result.Get("id").String()
	model := result.Get("model").String()
	stopReason := mapStopReason(result.Get("stop_reason").String())

	// Build message content from content blocks.
	var contentText strings.Builder
	var toolCalls []json.RawMessage
	result.Get("content").ForEach(func(_, block gjson.Result) bool {
		switch block.Get("type").String() {
		case "text":
			contentText.WriteString(block.Get("text").String())
		case "tool_use":
			tc, _ := json.Marshal(map[string]any{
				"id":   block.Get("id").String(),
				"type": "function",
				"function": map[string]any{
					"name":      block.Get("name").String(),
					"arguments": block.Get("input").Raw,
				},
			})
			toolCalls = append(toolCalls, tc)
		}
		return true
	})

	msg := gateway.Message{Role: "assistant"}
	if contentText.Len() > 0 {
		ct, _ := json.Marshal(contentText.String())
		msg.Content = ct
	}
	if len(toolCalls) > 0 {
		tc, _ := json.Marshal(toolCalls)
		msg.ToolCalls = tc
		if stopReason == "" {
			stopReason = "tool_calls"
		}
	}

	var usage *gateway.Usage
	if u := result.Get("usage"); u.Exists() {
		usage = &gateway.Usage{
			PromptTokens:     int(u.Get("input_tokens").Int()),
			CompletionTokens: int(u.Get("output_tokens").Int()),
			TotalTokens:      int(u.Get("input_tokens").Int()) + int(u.Get("output_tokens").Int()),
		}
	}

	return &gateway.ChatResponse{
		ID:      id,
		Object:  "chat.completion",
		Model:   model,
		Choices: []gateway.Choice{{Index: 0, Message: msg, FinishReason: stopReason}},
		Usage:   usage,
	}, nil
}

// mapStopReason converts Anthropic stop reasons to OpenAI finish reasons.
func mapStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}
