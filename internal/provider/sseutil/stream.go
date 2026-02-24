package sseutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tidwall/gjson"

	gateway "github.com/eugener/gandalf/internal"
)

// ReadSSEStream reads SSE lines from resp and sends them as StreamChunks on ch.
// It handles the standard SSE "[DONE]" sentinel and extracts usage from the
// final chunk. Used by openai and ollama adapters that share this SSE format.
// The channel is closed when done.
func ReadSSEStream(ctx context.Context, providerName string, resp *http.Response, ch chan<- gateway.StreamChunk) {
	defer close(ch)
	defer resp.Body.Close()

	scanner := NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		_, data, ok := ParseSSELine(line)
		if !ok {
			continue
		}
		if data == "[DONE]" {
			ch <- gateway.StreamChunk{Done: true}
			return
		}

		chunk := gateway.StreamChunk{Data: []byte(data)}
		// Extract usage from final chunk if present.
		if u := gjson.GetBytes(chunk.Data, "usage"); u.Exists() && u.Type == gjson.JSON {
			var usage gateway.Usage
			if json.Unmarshal([]byte(u.Raw), &usage) == nil && usage.TotalTokens > 0 {
				chunk.Usage = &usage
			}
		}

		select {
		case ch <- chunk:
		case <-ctx.Done():
			ch <- gateway.StreamChunk{Err: ctx.Err()}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- gateway.StreamChunk{Err: fmt.Errorf("%s: read stream: %w", providerName, err)}
	}
}
