package server

import (
	"encoding/json"
	"net/http"

	gateway "github.com/eugener/gandalf/internal"
)

// handleEmbeddings decodes an embedding request and forwards it to the proxy.
func (s *server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req gateway.EmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body: "+err.Error()))
		return
	}

	resp, err := s.deps.Proxy.Embeddings(r.Context(), &req)
	if err != nil {
		status := errorStatus(err)
		writeJSON(w, status, errorResponse(err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
