package gateway

import "errors"

// Sentinel errors for the gateway domain.
var (
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrRateLimited     = errors.New("rate limited")
	ErrQuotaExceeded   = errors.New("quota exceeded")
	ErrModelNotAllowed = errors.New("model not allowed")
	ErrProviderError   = errors.New("provider error")
	ErrBadRequest      = errors.New("bad request")
	ErrKeyExpired      = errors.New("api key expired")
	ErrKeyBlocked      = errors.New("api key blocked")
)
