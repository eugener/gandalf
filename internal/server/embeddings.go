package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	gateway "github.com/eugener/gandalf/internal"
)

// handleEmbeddings decodes an embedding request and forwards it to the proxy.
func (s *server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	buf := bodyPool.Get().(*bytes.Buffer)
	buf.Reset()
	_, err := buf.ReadFrom(r.Body)
	if err != nil {
		bodyPool.Put(buf)
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body: "+err.Error()))
		return
	}
	var req gateway.EmbeddingRequest
	if err := json.Unmarshal(buf.Bytes(), &req); err != nil {
		bodyPool.Put(buf)
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body: "+err.Error()))
		return
	}
	bodyPool.Put(buf)

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
