package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	gateway "github.com/eugener/gandalf/internal"
)

func (s *server) handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	var req gateway.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body: "+err.Error()))
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
