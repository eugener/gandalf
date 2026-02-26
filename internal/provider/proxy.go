// Package provider implements the provider registry for LLM provider adapters.
//
// This file provides shared helpers: NewTransport for HTTP client setup and
// ForwardRequest for native HTTP passthrough.
package provider

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/dnscache"
)

// NewTransport returns a tuned *http.Transport with connection pooling and
// optional DNS caching. Set forceHTTP2 to true for remote HTTPS APIs, false
// for local HTTP/1.1 servers (e.g. Ollama).
func NewTransport(resolver *dnscache.Resolver, forceHTTP2 bool) *http.Transport {
	t := &http.Transport{
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     200,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   forceHTTP2,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	if resolver != nil {
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := resolver.LookupHost(ctx, host)
			if err != nil {
				return nil, err
			}
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
		}
	}
	return t
}

// hopByHop headers that must not be forwarded between client and upstream.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// ForwardRequest proxies a raw HTTP request to a provider's upstream API.
// It builds the target URL from baseURL + path (+ original query string),
// copies non-hop-by-hop headers, calls setAuth to inject provider-specific
// credentials, and streams the response back with flush-on-read for SSE/NDJSON.
func ForwardRequest(ctx context.Context, client *http.Client, baseURL string,
	setAuth func(http.Header), w http.ResponseWriter, r *http.Request, path string) error {

	// Build target URL: baseURL + path + original query string.
	targetURL := baseURL + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	outReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, r.Body)
	if err != nil {
		return fmt.Errorf("native proxy: create request: %w", err)
	}

	// Copy non-hop-by-hop headers from the client request.
	for key, vals := range r.Header {
		if _, hop := hopByHopHeaders[key]; hop {
			continue
		}
		// Skip auth headers -- setAuth will add the correct ones.
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "x-api-key" ||
			lower == "x-goog-api-key" || lower == "api-key" {
			continue
		}
		outReq.Header[key] = vals
	}

	// Apply provider-specific auth (if any; auth may be in the transport chain).
	if setAuth != nil {
		setAuth(outReq.Header)
	}

	resp, err := client.Do(outReq)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return fmt.Errorf("native proxy: do request: %w", err)
	}
	defer resp.Body.Close()

	// Copy response headers.
	for key, vals := range resp.Header {
		if _, hop := hopByHopHeaders[key]; hop {
			continue
		}
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream response body with flush-on-read for SSE/NDJSON.
	flusher, canFlush := w.(http.Flusher)
	ct := resp.Header.Get("Content-Type")
	needsFlush := canFlush && (strings.Contains(ct, "text/event-stream") ||
		strings.Contains(ct, "application/x-ndjson") ||
		strings.Contains(ct, "application/stream+json"))

	if needsFlush {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return fmt.Errorf("native proxy: write response: %w", writeErr)
				}
				flusher.Flush()
			}
			if readErr != nil {
				if readErr == io.EOF {
					return nil
				}
				return fmt.Errorf("native proxy: read response: %w", readErr)
			}
		}
	}

	// Non-streaming: bulk copy. Cap at 32 MB to prevent a malicious or
	// misconfigured upstream from causing unbounded memory allocation.
	const maxResponseBody = 32 << 20
	if _, err := io.Copy(w, io.LimitReader(resp.Body, maxResponseBody)); err != nil {
		return fmt.Errorf("native proxy: copy response: %w", err)
	}
	return nil
}
