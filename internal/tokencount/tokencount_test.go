package tokencount

import (
	"testing"

	gateway "github.com/eugener/gandalf/internal"
)

func TestCounter_EstimateRequest(t *testing.T) {
	t.Parallel()
	c := NewCounter()

	tests := []struct {
		name     string
		model    string
		messages []gateway.Message
		wantMin  int
		wantMax  int
	}{
		{
			name:  "single short message",
			model: "gpt-4o",
			messages: []gateway.Message{
				{Role: "user", Content: []byte(`"hello"`)},
			},
			wantMin: 5,
			wantMax: 20,
		},
		{
			name:  "multiple messages",
			model: "gpt-4o",
			messages: []gateway.Message{
				{Role: "system", Content: []byte(`"You are helpful."`)},
				{Role: "user", Content: []byte(`"Explain quantum computing."`)},
			},
			wantMin: 15,
			wantMax: 40,
		},
		{
			name:     "empty messages",
			model:    "gpt-4o",
			messages: nil,
			wantMin:  1,
			wantMax:  10,
		},
		{
			name:  "unknown model fallback",
			model: "claude-3-opus",
			messages: []gateway.Message{
				{Role: "user", Content: []byte(`"test"`)},
			},
			wantMin: 5,
			wantMax: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := c.EstimateRequest(tt.model, tt.messages)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("EstimateRequest() = %d, want [%d, %d]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestCounter_CountText(t *testing.T) {
	t.Parallel()
	c := NewCounter()

	got := c.CountText("gpt-4o", "Hello, world!")
	if got < 1 {
		t.Errorf("CountText() = %d, want >= 1", got)
	}
}

func TestCounter_CountTextEmpty(t *testing.T) {
	t.Parallel()
	c := NewCounter()

	got := c.CountText("gpt-4o", "")
	if got != 1 {
		t.Errorf("CountText('') = %d, want 1 (min)", got)
	}
}

func TestCounter_MessageWithName(t *testing.T) {
	t.Parallel()
	c := NewCounter()

	msgs := []gateway.Message{{
		Role:    "user",
		Content: []byte(`"hello"`),
		Name:    "alice",
	}}
	got := c.EstimateRequest("gpt-4o", msgs)
	if got < 5 {
		t.Errorf("EstimateRequest with name = %d, want >= 5", got)
	}
}

func TestCounter_MessageWithToolCalls(t *testing.T) {
	t.Parallel()
	c := NewCounter()

	msgs := []gateway.Message{{
		Role:       "assistant",
		Content:    []byte(`""`),
		ToolCalls:  []byte(`[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]`),
		ToolCallID: "call_1",
	}}
	got := c.EstimateRequest("gpt-4o", msgs)
	if got < 10 {
		t.Errorf("EstimateRequest with tool calls = %d, want >= 10", got)
	}
}
