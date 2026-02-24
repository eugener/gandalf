package sseutil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	gateway "github.com/eugener/gandalf/internal"
)

func TestReadSSEStream(t *testing.T) {
	t.Parallel()

	body := "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	ch := make(chan gateway.StreamChunk, 8)
	go ReadSSEStream(context.Background(), "test", resp, ch)

	var chunks []gateway.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if string(chunks[0].Data) == "" {
		t.Error("first chunk should have data")
	}
	if !chunks[2].Done {
		t.Error("last chunk should be Done")
	}
}

func TestReadSSEStreamUsage(t *testing.T) {
	t.Parallel()

	body := `data: {"id":"1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}` + "\n\n" +
		"data: [DONE]\n\n"

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	ch := make(chan gateway.StreamChunk, 8)
	go ReadSSEStream(context.Background(), "test", resp, ch)

	var chunks []gateway.StreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Usage == nil {
		t.Fatal("first chunk should have usage")
	}
	if chunks[0].Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15", chunks[0].Usage.TotalTokens)
	}
}

func TestReadSSEStreamContextCancel(t *testing.T) {
	t.Parallel()

	// Use a pipe so we can control when data arrives.
	pr, pw := io.Pipe()
	resp := &http.Response{Body: pr}

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan gateway.StreamChunk, 8)
	go ReadSSEStream(ctx, "test", resp, ch)

	// Write one chunk.
	pw.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	c := <-ch
	if len(c.Data) == 0 {
		t.Error("expected data")
	}

	// Cancel and close pipe.
	cancel()
	pw.Close()

	// Drain remaining.
	for c := range ch {
		if c.Err != nil {
			return // expected
		}
	}
}

func TestReadSSEStreamScannerError(t *testing.T) {
	t.Parallel()

	// errReader always returns an error.
	resp := &http.Response{Body: io.NopCloser(&errReader{})}
	ch := make(chan gateway.StreamChunk, 8)
	go ReadSSEStream(context.Background(), "test", resp, ch)

	var gotErr bool
	for c := range ch {
		if c.Err != nil {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("expected error chunk from broken reader")
	}
}

type errReader struct{}

func (e *errReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
