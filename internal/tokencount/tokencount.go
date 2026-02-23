// Package tokencount provides token estimation for TPM rate limiting and usage recording.
// Uses a character-based heuristic (~4 chars per token for English) which is sufficient
// for rate limiting. Can be replaced with tiktoken for exact counts if needed.
package tokencount

import (
	gateway "github.com/eugener/gandalf/internal"
)

// Counter estimates token counts for requests and text.
type Counter struct{}

// NewCounter creates a new Counter.
func NewCounter() *Counter {
	return &Counter{}
}

// EstimateRequest estimates the total token count for a chat completion request.
// Accounts for message overhead (role, formatting) per the OpenAI tokenization spec.
func (c *Counter) EstimateRequest(model string, messages []gateway.Message) int {
	total := 0
	overhead := messageOverhead(model)
	for _, m := range messages {
		total += overhead
		total += estimateTokens(m.Role)
		total += estimateTokens(string(m.Content))
		if m.Name != "" {
			total += estimateTokens(m.Name) + 1 // name costs 1 extra token
		}
		if len(m.ToolCalls) > 0 {
			total += estimateTokens(string(m.ToolCalls))
		}
		if m.ToolCallID != "" {
			total += estimateTokens(m.ToolCallID)
		}
	}
	total += 3 // every reply is primed with <|start|>assistant<|message|>
	return max(total, 1)
}

// CountText estimates tokens for a plain text string.
func (c *Counter) CountText(_ string, text string) int {
	return max(estimateTokens(text), 1)
}

// estimateTokens uses ~4 characters per token heuristic.
// This is a reasonable approximation for English text with GPT-family tokenizers.
func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	// ~4 bytes per token for English; ceil division.
	return (len(s) + 3) / 4
}

// messageOverhead returns per-message token overhead.
// GPT-4o and newer use 4 tokens per message; older models use 3.
func messageOverhead(_ string) int {
	return 4
}
