package server

import (
	"bytes"
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

const streamPayload = `{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":true}`

func BenchmarkChatCompletionStream(b *testing.B) {
	h := newTestHandler()

	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(streamPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer gnd_test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
	}
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

// ---------------------------------------------------------------------------
// Handler-only microbenchmarks
//
// The benchmarks above measure end-to-end including httptest.NewRequest,
// httptest.NewRecorder, and Header.Set overhead (~8-10 allocs/iter).
// The variants below minimise test-infra cost to isolate actual handler allocs:
//   - Pre-allocated header map (avoids Header.Set canonicalization)
//   - bytes.NewReader (seekable, avoids strings.NewReader per iter)
//   - discardResponseWriter (avoids NewRecorder's bytes.Buffer alloc)
// ---------------------------------------------------------------------------

// discardResponseWriter is a minimal ResponseWriter for benchmarks.
// Captures status code, discards body, reuses header map between iterations.
type discardResponseWriter struct {
	hdr  http.Header
	code int
}

func (w *discardResponseWriter) Header() http.Header        { return w.hdr }
func (w *discardResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *discardResponseWriter) WriteHeader(code int)        { w.code = code }

// Flush implements http.Flusher so SSE streaming works through middleware.
func (w *discardResponseWriter) Flush() {}

func (w *discardResponseWriter) reset() {
	clear(w.hdr)
	w.code = http.StatusOK
}

func BenchmarkChatCompletionHandler(b *testing.B) {
	h := newTestHandler()
	body := []byte(chatPayload)
	hdr := http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer gnd_test"},
	}
	w := &discardResponseWriter{hdr: make(http.Header, 8), code: http.StatusOK}

	b.ResetTimer()
	for b.Loop() {
		req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		req.Header = hdr
		w.reset()
		h.ServeHTTP(w, req)
		if w.code != http.StatusOK {
			b.Fatalf("status = %d, want 200", w.code)
		}
	}
}

func BenchmarkChatCompletionStreamHandler(b *testing.B) {
	h := newTestHandler()
	body := []byte(streamPayload)
	hdr := http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer gnd_test"},
	}
	w := &discardResponseWriter{hdr: make(http.Header, 8), code: http.StatusOK}

	b.ResetTimer()
	for b.Loop() {
		req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		req.Header = hdr
		w.reset()
		h.ServeHTTP(w, req)
		if w.code != http.StatusOK {
			b.Fatalf("status = %d, want 200", w.code)
		}
	}
}

func BenchmarkHealthzHandler(b *testing.B) {
	h := newTestHandler()
	w := &discardResponseWriter{hdr: make(http.Header, 4), code: http.StatusOK}

	b.ResetTimer()
	for b.Loop() {
		req, _ := http.NewRequest(http.MethodGet, "/healthz", nil)
		w.reset()
		h.ServeHTTP(w, req)
		if w.code != http.StatusOK {
			b.Fatalf("status = %d, want 200", w.code)
		}
	}
}
