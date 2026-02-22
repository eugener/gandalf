// Package gemini implements the gateway.Provider adapter for the Google Gemini API.
package gemini

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
)

// geminiRequest is the Gemini generateContent request body.
type geminiRequest struct {
	Contents          []geminiContent          `json:"contents"`
	SystemInstruction *geminiContent           `json:"systemInstruction,omitempty"`
	Tools             []geminiTool             `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string          `json:"text,omitempty"`
	FunctionCall     json.RawMessage `json:"functionCall,omitempty"`
	FunctionResponse json.RawMessage `json:"functionResponse,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations json.RawMessage `json:"functionDeclarations,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	MaxOutputTokens *int            `json:"maxOutputTokens,omitempty"`
	StopSequences   json.RawMessage `json:"stopSequences,omitempty"`
}

// translateRequest converts an OpenAI ChatRequest to a Gemini generateContent request.
func translateRequest(req *gateway.ChatRequest) *geminiRequest {
	out := &geminiRequest{}

	// Generation config.
	if req.Temperature != nil || req.TopP != nil || req.MaxTokens != nil || len(req.Stop) > 0 {
		out.GenerationConfig = &geminiGenerationConfig{
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			MaxOutputTokens: req.MaxTokens,
			StopSequences:   req.Stop,
		}
	}

	// Tools: extract function declarations from OpenAI tools format.
	if len(req.Tools) > 0 {
		var openaiTools []struct {
			Function json.RawMessage `json:"function"`
		}
		if json.Unmarshal(req.Tools, &openaiTools) == nil && len(openaiTools) > 0 {
			var decls []json.RawMessage
			for _, t := range openaiTools {
				if t.Function != nil {
					decls = append(decls, t.Function)
				}
			}
			if len(decls) > 0 {
				raw, _ := json.Marshal(decls)
				out.Tools = []geminiTool{{FunctionDeclarations: raw}}
			}
		}
	}

	// Messages.
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			text := extractText(m.Content)
			out.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: text}},
			}
		case "user":
			text := extractText(m.Content)
			out.Contents = append(out.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: text}},
			})
		case "assistant":
			text := extractText(m.Content)
			out.Contents = append(out.Contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: text}},
			})
		case "tool":
			// Tool results map to functionResponse parts.
			fr, _ := json.Marshal(map[string]any{
				"name":     m.ToolCallID,
				"response": json.RawMessage(m.Content),
			})
			out.Contents = append(out.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{FunctionResponse: fr}},
			})
		}
	}

	return out
}

// translateResponse converts a Gemini generateContent JSON response to an
// OpenAI-format ChatResponse.
func translateResponse(data []byte, requestModel string) (*gateway.ChatResponse, error) {
	r := gjson.ParseBytes(data)

	stopReason := mapStopReason(r.Get("candidates.0.finishReason").String())

	// Extract content from first candidate.
	var contentText strings.Builder
	var toolCalls []json.RawMessage
	r.Get("candidates.0.content.parts").ForEach(func(_, part gjson.Result) bool {
		if text := part.Get("text"); text.Exists() {
			contentText.WriteString(text.String())
		}
		if fc := part.Get("functionCall"); fc.Exists() {
			tc, _ := json.Marshal(map[string]any{
				"id":   fc.Get("name").String(), // Gemini doesn't have separate IDs
				"type": "function",
				"function": map[string]any{
					"name":      fc.Get("name").String(),
					"arguments": fc.Get("args").Raw,
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
	if u := r.Get("usageMetadata"); u.Exists() {
		usage = &gateway.Usage{
			PromptTokens:     int(u.Get("promptTokenCount").Int()),
			CompletionTokens: int(u.Get("candidatesTokenCount").Int()),
			TotalTokens:      int(u.Get("totalTokenCount").Int()),
		}
	}

	return &gateway.ChatResponse{
		ID:      "gemini-" + requestModel,
		Object:  "chat.completion",
		Model:   requestModel,
		Choices: []gateway.Choice{{Index: 0, Message: msg, FinishReason: stopReason}},
		Usage:   usage,
	}, nil
}

// mapStopReason converts Gemini finish reasons to OpenAI finish reasons.
func mapStopReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	default:
		return reason
	}
}

// extractText extracts a text string from a JSON content field which may be
// a raw string or a structured content array.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try as quoted string first.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// Try as array of content parts (OpenAI multimodal format).
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return string(raw)
}
