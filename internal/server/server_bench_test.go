package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// TextHandler(io.Discard) still processes/formats attrs (accurate alloc count)
	// but suppresses log output during benchmarks. Do NOT use a no-op handler with
	// Enabled()=false -- that skips all work, undercounting allocations.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

const chatPayload = `{"model":"gpt-4o","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"Explain the theory of relativity in one sentence."}]}`

func BenchmarkChatCompletion(b *testing.B) {
	h := newTestHandler()

	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer gnd_test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkChatCompletionParallel(b *testing.B) {
	h := newTestHandler()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatPayload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer gnd_test")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}
		}
	})
}

func BenchmarkHealthz(b *testing.B) {
	h := newTestHandler()

	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}
