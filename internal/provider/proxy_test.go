package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/dnscache"
)

func TestNewTransportNilResolver(t *testing.T) {
	t.Parallel()

	tr := NewTransport(nil, false)

	if tr.MaxIdleConnsPerHost != 100 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 100", tr.MaxIdleConnsPerHost)
	}
	if tr.MaxConnsPerHost != 200 {
		t.Errorf("MaxConnsPerHost = %d, want 200", tr.MaxConnsPerHost)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 90s", tr.IdleConnTimeout)
	}
	if tr.TLSHandshakeTimeout != 5*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 5s", tr.TLSHandshakeTimeout)
	}
	if tr.DialContext != nil {
		t.Error("DialContext should be nil when resolver is nil")
	}
}

func TestNewTransportWithResolver(t *testing.T) {
	t.Parallel()

	resolver := &dnscache.Resolver{}
	tr := NewTransport(resolver, false)

	if tr.DialContext == nil {
		t.Error("DialContext should be set when resolver is non-nil")
	}
}

func TestNewTransportForceHTTP2(t *testing.T) {
	t.Parallel()

	trHTTP2 := NewTransport(nil, true)
	if !trHTTP2.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be true when forceHTTP2=true")
	}

	trHTTP1 := NewTransport(nil, false)
	if trHTTP1.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be false when forceHTTP2=false")
	}
}

func TestForwardRequest(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test/path" {
			t.Errorf("path = %q, want /test/path", r.URL.Path)
		}
		if r.URL.RawQuery != "foo=bar" {
			t.Errorf("query = %q, want foo=bar", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "response-header")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test/path?foo=bar", strings.NewReader(`{"hello":"world"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer client-key") // should be stripped

	err := ForwardRequest(context.Background(), upstream.Client(), upstream.URL, func(h http.Header) {
		h.Set("Authorization", "Bearer test-key")
	}, rec, req, "/test/path")

	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Header().Get("X-Custom") != "response-header" {
		t.Errorf("missing response header X-Custom")
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Errorf("body = %q, want to contain hello", rec.Body.String())
	}
}

func TestForwardRequestSSEFlush(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		io.WriteString(w, "data: chunk1\n\n")
		flusher.Flush()
		io.WriteString(w, "data: chunk2\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := ForwardRequest(context.Background(), upstream.Client(), upstream.URL, func(h http.Header) {}, rec, req, "/stream")

	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "chunk1") || !strings.Contains(body, "chunk2") {
		t.Errorf("body = %q, want both chunks", body)
	}
}

func TestForwardRequestUpstreamError(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"bad request"}`)
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("{}"))

	err := ForwardRequest(context.Background(), upstream.Client(), upstream.URL, func(h http.Header) {}, rec, req, "/test")

	if err != nil {
		t.Fatal(err)
	}
	// Upstream error status should be forwarded
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestForwardRequestStripsHopByHop(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connection") != "" {
			t.Error("Connection header should be stripped")
		}
		if r.Header.Get("Keep-Alive") != "" {
			t.Error("Keep-Alive header should be stripped")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Keep-Alive", "timeout=5")

	err := ForwardRequest(context.Background(), upstream.Client(), upstream.URL, func(h http.Header) {}, rec, req, "/test")
	if err != nil {
		t.Fatal(err)
	}
}
