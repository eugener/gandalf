package anthropic

import (
	"bytes"
	"encoding/base64"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"

	gateway "github.com/eugener/gandalf/internal"
)

// encodeEvent builds a binary event stream frame with a base64-wrapped
// Anthropic event JSON payload.
func encodeEvent(t *testing.T, eventType, anthropicJSON string) []byte {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString([]byte(anthropicJSON))
	payload := []byte(`{"bytes":"` + b64 + `"}`)

	msg := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("event")},
			{Name: ":event-type", Value: eventstream.StringValue(eventType)},
		},
		Payload: payload,
	}

	var buf bytes.Buffer
	encoder := eventstream.NewEncoder()
	if err := encoder.Encode(&buf, msg); err != nil {
		t.Fatalf("encode event: %v", err)
	}
	return buf.Bytes()
}

// encodeException builds a binary event stream exception frame.
func encodeException(t *testing.T, exType, message string) []byte {
	t.Helper()
	msg := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: ":message-type", Value: eventstream.StringValue("exception")},
			{Name: ":exception-type", Value: eventstream.StringValue(exType)},
		},
		Payload: []byte(message),
	}
	var buf bytes.Buffer
	encoder := eventstream.NewEncoder()
	if err := encoder.Encode(&buf, msg); err != nil {
		t.Fatalf("encode exception: %v", err)
	}
	return buf.Bytes()
}

func TestReadBedrockStream(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	stream.Write(encodeEvent(t, "message_start",
		`{"type":"message_start","message":{"id":"msg_01","model":"anthropic.claude-3-5-sonnet","usage":{"input_tokens":10}}}`))
	stream.Write(encodeEvent(t, "content_block_delta",
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`))
	stream.Write(encodeEvent(t, "content_block_delta",
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`))
	stream.Write(encodeEvent(t, "message_delta",
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`))
	stream.Write(encodeEvent(t, "message_stop",
		`{"type":"message_stop"}`))

	ch := make(chan gateway.StreamChunk, 16)
	go readBedrockStream(t.Context(), io.NopCloser(&stream), ch)

	var chunks []gateway.StreamChunk
	for c := range ch {
		if c.Err != nil {
			t.Fatalf("unexpected error: %v", c.Err)
		}
		chunks = append(chunks, c)
	}

	// Expect: role chunk, 2 text deltas, finish chunk, usage chunk, done = 6
	if len(chunks) != 6 {
		t.Fatalf("got %d chunks, want 6", len(chunks))
	}

	// Last chunk should be Done.
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Error("last chunk should be Done")
	}

	// Second-to-last should have usage.
	usageChunk := chunks[len(chunks)-2]
	if usageChunk.Usage == nil {
		t.Fatal("expected usage in second-to-last chunk")
	}
	if usageChunk.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15", usageChunk.Usage.TotalTokens)
	}
}

func TestReadBedrockStreamException(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	stream.Write(encodeException(t, "throttlingException", "rate limit exceeded"))

	ch := make(chan gateway.StreamChunk, 4)
	go readBedrockStream(t.Context(), io.NopCloser(&stream), ch)

	var gotErr bool
	for c := range ch {
		if c.Err != nil {
			gotErr = true
			if c.Err.Error() == "" {
				t.Error("error should not be empty")
			}
		}
	}
	if !gotErr {
		t.Error("expected error chunk for exception frame")
	}
}

func TestExtractEventBytes(t *testing.T) {
	t.Parallel()

	original := `{"type":"message_start","message":{"id":"msg_01"}}`
	b64 := base64.StdEncoding.EncodeToString([]byte(original))
	payload := []byte(`{"bytes":"` + b64 + `"}`)

	decoded, err := extractEventBytes(payload)
	if err != nil {
		t.Fatalf("extractEventBytes: %v", err)
	}
	if string(decoded) != original {
		t.Errorf("decoded = %q, want %q", string(decoded), original)
	}
}

func TestExtractEventBytesMissing(t *testing.T) {
	t.Parallel()

	_, err := extractEventBytes([]byte(`{"other":"value"}`))
	if err == nil {
		t.Fatal("expected error for missing bytes field")
	}
}

func TestHeaderValue(t *testing.T) {
	t.Parallel()

	headers := eventstream.Headers{
		{Name: ":message-type", Value: eventstream.StringValue("event")},
		{Name: ":event-type", Value: eventstream.StringValue("chunk")},
	}

	if got := headerValue(headers, ":message-type"); got != "event" {
		t.Errorf("headerValue(:message-type) = %q, want event", got)
	}
	if got := headerValue(headers, ":event-type"); got != "chunk" {
		t.Errorf("headerValue(:event-type) = %q, want chunk", got)
	}
	if got := headerValue(headers, "missing"); got != "" {
		t.Errorf("headerValue(missing) = %q, want empty", got)
	}
}
