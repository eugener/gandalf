package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

func (s *server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var req gateway.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body: "+err.Error()))
		return
	}

	if req.Stream {
		s.handleChatCompletionStream(w, r, &req)
		return
	}

	resp, err := s.deps.Proxy.ChatCompletion(r.Context(), &req)
	if err != nil {
		status := errorStatus(err)
		writeJSON(w, status, errorResponse(err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleChatCompletionStream handles SSE streaming chat completion requests.
func (s *server) handleChatCompletionStream(w http.ResponseWriter, r *http.Request, req *gateway.ChatRequest) {
	ch, err := s.deps.Proxy.ChatCompletionStream(r.Context(), req)
	if err != nil {
		status := errorStatus(err)
		writeJSON(w, status, errorResponse(err.Error()))
		return
	}

	writeSSEHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		slog.Error("ResponseWriter does not implement http.Flusher")
		return
	}
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				writeSSEDone(w)
				flusher.Flush()
				return
			}
			if chunk.Err != nil {
				slog.LogAttrs(r.Context(), slog.LevelError, "stream error",
					slog.String("error", chunk.Err.Error()),
				)
				writeSSEDone(w)
				flusher.Flush()
				return
			}
			if chunk.Done {
				writeSSEDone(w)
				flusher.Flush()
				return
			}
			writeSSEData(w, chunk.Data)
			flusher.Flush()

		case <-keepAlive.C:
			writeSSEKeepAlive(w)
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

type apiError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func errorResponse(msg string) apiError {
	var e apiError
	e.Error.Message = msg
	e.Error.Type = "invalid_request_error"
	return e
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, gateway.ErrUnauthorized), errors.Is(err, gateway.ErrKeyExpired):
		return http.StatusUnauthorized
	case errors.Is(err, gateway.ErrForbidden), errors.Is(err, gateway.ErrModelNotAllowed), errors.Is(err, gateway.ErrKeyBlocked):
		return http.StatusForbidden
	case errors.Is(err, gateway.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, gateway.ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, gateway.ErrBadRequest):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// jsonCT is a pre-allocated header value slice. Direct map assignment
// (w.Header()["Content-Type"] = jsonCT) avoids the []string{v} alloc
// that Header.Set creates on every call. Saves 1 alloc/req.
var jsonCT = []string{"application/json"}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header()["Content-Type"] = jsonCT
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}
