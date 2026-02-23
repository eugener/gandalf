package server

import (
	"net/http"
	"time"
)

// handleListModels aggregates models from all providers and returns
// an OpenAI-compatible model list response.
func (s *server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.deps.Proxy.ListModels(r.Context())
	if err != nil {
		writeUpstreamError(w, r.Context(), err)
		return
	}

	now := time.Now().Unix()
	data := make([]modelEntry, len(models))
	for i, m := range models {
		data[i] = modelEntry{
			ID:      m,
			Object:  "model",
			Created: now,
			OwnedBy: "system",
		}
	}

	writeJSON(w, http.StatusOK, modelListResponse{
		Object: "list",
		Data:   data,
	})
}

type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type modelListResponse struct {
	Object string       `json:"object"`
	Data   []modelEntry `json:"data"`
}
