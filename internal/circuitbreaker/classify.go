package circuitbreaker

import (
	"context"
	"errors"
	"net"
	"os"
)

// httpStatusError is an interface for errors carrying an HTTP status code.
// Matches the same interface used in internal/app/proxy.go.
type httpStatusError interface {
	HTTPStatus() int
}

// ClassifyError returns the error weight for circuit breaker tracking.
//
// Weights:
//   - 429 (rate limited) -> 0.5
//   - 500, 502, 503, 504 -> 1.0
//   - timeout (deadline exceeded) -> 1.5
//   - 4xx (except 429) -> 0.0 (client errors, not provider fault)
//   - network errors (non-timeout) -> 1.0
//   - nil -> 0.0
func ClassifyError(err error) float64 {
	if err == nil {
		return 0
	}

	// Check for timeout errors first (highest weight).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return 1.5
	}

	// Check for HTTP status code.
	var he httpStatusError
	if errors.As(err, &he) {
		return classifyStatus(he.HTTPStatus())
	}

	// Check for network errors (non-timeout, already handled above).
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return 1.0
	}

	// Generic errors (e.g. connection refused) -> treat as server fault.
	return 1.0
}

// classifyStatus returns the error weight for an HTTP status code.
func classifyStatus(code int) float64 {
	switch {
	case code == 429:
		return 0.5
	case code >= 500 && code <= 504:
		return 1.0
	case code >= 400 && code < 500:
		return 0.0
	default:
		return 0.0
	}
}
