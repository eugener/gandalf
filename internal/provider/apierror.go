// Package provider contains shared utilities for LLM provider adapters.
package provider

import (
	"fmt"
	"io"
	"net/http"
)

// APIError represents an error response from an upstream LLM provider.
// It satisfies the httpStatusError interface used by proxy failover logic.
type APIError struct {
	Provider   string
	StatusCode int
	Body       string
}

// Error returns a formatted error string including provider, status, and body.
func (e *APIError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.StatusCode, e.Body)
}

// HTTPStatus returns the HTTP status code for failover decisions.
func (e *APIError) HTTPStatus() int { return e.StatusCode }

// ParseAPIError reads up to 4KB from the response body and returns an APIError.
func ParseAPIError(provider string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &APIError{Provider: provider, StatusCode: resp.StatusCode, Body: string(body)}
}
