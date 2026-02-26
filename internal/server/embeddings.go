package server

import (
	"net/http"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// handleEmbeddings decodes an embedding request and forwards it to the proxy.
func (s *server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req gateway.EmbeddingRequest
	if !decodeRequestBody(w, r, &req) {
		return
	}

	// Model allowlist check.
	identity := gateway.IdentityFromContext(r.Context())
	if identity != nil && !identity.IsModelAllowed(req.Model) {
		writeJSON(w, http.StatusForbidden, errorResponse("model not allowed"))
		return
	}

	// TPM rate limit for embeddings (rough estimate).
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
