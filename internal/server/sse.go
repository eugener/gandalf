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

// sseHeaders is a pre-allocated header value slice for SSE responses.
var sseHeaders = []string{"text/event-stream"}

// writeSSEHeaders sets the response headers for an SSE stream.
func writeSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h["Content-Type"] = sseHeaders
	h["Cache-Control"] = []string{"no-cache"}
	h["Connection"] = []string{"keep-alive"}
	h["X-Accel-Buffering"] = []string{"no"}
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
