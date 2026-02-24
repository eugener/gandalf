package server

import (
	"encoding/json"
	"net/http"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// handleEmbeddings decodes an embedding request and forwards it to the proxy.
func (s *server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var req gateway.EmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body: "+err.Error()))
		return
	}

	// TPM rate limit for embeddings (rough estimate).
	identity := gateway.IdentityFromContext(r.Context())
	estimated := int64(100)
	if !s.consumeTPM(w, identity, estimated) {
		return
	}

	start := time.Now()
	resp, err := s.deps.Proxy.Embeddings(r.Context(), &req)
	elapsed := time.Since(start)
	if err != nil {
		writeUpstreamError(w, r.Context(), err)
		return
	}

	s.adjustTPM(identity, estimated, resp.Usage)
	s.recordUsage(r, identity, req.Model, resp.Usage, elapsed, false)

	writeJSON(w, http.StatusOK, resp)
}
