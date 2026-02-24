package server

import (
	"net/http"
)

// Pre-allocated byte slices for SSE formatting. These avoid heap allocations
// on every write in the streaming hot path.
var (
	sseDataPrefix = []byte("data: ")
	sseNewline    = []byte("\n\n")
	sseDone       = []byte("data: [DONE]\n\n")
	sseKeepAlive  = []byte(": keep-alive\n\n")
)

// Pre-allocated header value slices for SSE responses.
// Direct map assignment avoids the []string{v} alloc that Header.Set creates.
var (
	sseHeaders      = []string{"text/event-stream"}
	sseCacheControl = []string{"no-cache"}
	sseConnection   = []string{"keep-alive"}
	sseAccelBuf     = []string{"no"}
)

// writeSSEHeaders sets the response headers for an SSE stream.
func writeSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h["Content-Type"] = sseHeaders
	h["Cache-Control"] = sseCacheControl
	h["Connection"] = sseConnection
	h["X-Accel-Buffering"] = sseAccelBuf
	w.WriteHeader(http.StatusOK)
}

// writeSSEData writes a single SSE data frame: "data: <payload>\n\n".
func writeSSEData(w http.ResponseWriter, data []byte) {
	w.Write(sseDataPrefix)
	w.Write(data)
	w.Write(sseNewline)
}

// writeSSEDone writes the SSE stream termination sentinel: "data: [DONE]\n\n".
func writeSSEDone(w http.ResponseWriter) {
	w.Write(sseDone)
}

// writeSSEKeepAlive writes an SSE comment to keep the connection alive.
func writeSSEKeepAlive(w http.ResponseWriter) {
	w.Write(sseKeepAlive)
}
