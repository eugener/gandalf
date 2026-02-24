package server

import "net/http"

// Pre-allocated response body and header value slice.
// okBody avoids a []byte("ok") heap escape per call.
// plainCT avoids the []string{v} alloc from Header.Set (see proxy.go:jsonCT).
// Together they save 3 allocs/req per health endpoint.
var (
	okBody       = []byte("ok")
	notReadyBody = []byte("not ready")
	plainCT      = []string{"text/plain"}
)

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header()["Content-Type"] = plainCT
	w.WriteHeader(http.StatusOK)
	w.Write(okBody)
}

func (s *server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.deps.ReadyCheck != nil {
		if err := s.deps.ReadyCheck(r.Context()); err != nil {
			w.Header()["Content-Type"] = plainCT
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write(notReadyBody)
			return
		}
	}
	w.Header()["Content-Type"] = plainCT
	w.WriteHeader(http.StatusOK)
	w.Write(okBody)
}
