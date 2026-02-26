package server

import (
	"net/http/httptest"
	"testing"
)

func TestWriteSSEHeaders(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeSSEHeaders(rec)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestWriteSSEData(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeSSEData(rec, []byte(`{"id":"1"}`))

	want := "data: {\"id\":\"1\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestWriteSSEDone(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeSSEDone(rec)

	want := "data: [DONE]\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestWriteSSEKeepAlive(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeSSEKeepAlive(rec)

	want := ": keep-alive\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestWriteSSEError(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeSSEError(rec, "upstream stream error")

	want := "event: error\ndata: {\"error\":{\"message\":\"upstream stream error\",\"type\":\"stream_error\"}}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}
