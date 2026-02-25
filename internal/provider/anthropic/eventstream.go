package anthropic

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
)

// readBedrockStream reads AWS binary event stream frames from a Bedrock
// invoke-with-response-stream response body and emits OpenAI-format
// StreamChunks. Each frame's payload contains {"bytes":"<base64>"} where
// the decoded bytes are standard Anthropic event JSON.
func readBedrockStream(ctx context.Context, body io.ReadCloser, ch chan<- gateway.StreamChunk) {
	defer close(ch)
	defer body.Close()

	var state streamState
	decoder := eventstream.NewDecoder()

	for {
		msg, err := decoder.Decode(body, nil)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			ch <- gateway.StreamChunk{Err: fmt.Errorf("anthropic: decode event stream: %w", err)}
			return
		}

		msgType := headerValue(msg.Headers, ":message-type")
		if msgType == "exception" {
			errType := headerValue(msg.Headers, ":exception-type")
			if len(errType) > 64 {
				errType = errType[:64]
			}
			payload := msg.Payload
			if len(payload) > 512 {
				payload = payload[:512]
			}
			ch <- gateway.StreamChunk{Err: fmt.Errorf("anthropic: bedrock exception: %s: %s", errType, payload)}
			return
		}

		if msgType != "event" {
			continue
		}

		// Extract and base64-decode the event bytes from the payload.
		decoded, err := extractEventBytes(msg.Payload)
		if err != nil {
			ch <- gateway.StreamChunk{Err: fmt.Errorf("anthropic: extract event bytes: %w", err)}
			return
		}

		// Get the Anthropic event type and feed to the shared state machine.
		eventType := gjson.GetBytes(decoded, "type").String()
		if eventType == "" {
			continue
		}

		chunks := state.handleEvent(eventType, string(decoded))
		for _, c := range chunks {
			select {
			case ch <- c:
			case <-ctx.Done():
				ch <- gateway.StreamChunk{Err: ctx.Err()}
				return
			}
		}
	}
}

// headerValue extracts a string header value from event stream headers.
func headerValue(headers eventstream.Headers, name string) string {
	v := headers.Get(name)
	if v == nil {
		return ""
	}
	if sv, ok := v.(eventstream.StringValue); ok {
		return string(sv)
	}
	return ""
}

// extractEventBytes extracts and base64-decodes the "bytes" field from a
// Bedrock event stream payload. The payload format is {"bytes":"<base64>"}.
func extractEventBytes(payload []byte) ([]byte, error) {
	b64 := gjson.GetBytes(payload, "bytes").String()
	if b64 == "" {
		return nil, fmt.Errorf("missing bytes field in payload")
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return decoded, nil
}
