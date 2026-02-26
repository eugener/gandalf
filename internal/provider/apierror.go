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

// maxErrorBodyLen is the maximum body length included in Error() output.
// Keeps log lines safe from PII in upstream error bodies.
const maxErrorBodyLen = 256

// Error returns a formatted error string including provider, status, and
// a truncated body (max 256 chars) to avoid logging PII from upstream.
func (e *APIError) Error() string {
	body := e.Body
	if len(body) > maxErrorBodyLen {
		body = body[:maxErrorBodyLen] + "...(truncated)"
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.StatusCode, body)
}

// FullError returns the complete error string with the untruncated body.
func (e *APIError) FullError() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.StatusCode, e.Body)
}

// HTTPStatus returns the HTTP status code for failover decisions.
func (e *APIError) HTTPStatus() int { return e.StatusCode }

// ParseAPIError reads up to 4KB from the response body and returns an APIError.
func ParseAPIError(provider string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &APIError{Provider: provider, StatusCode: resp.StatusCode, Body: string(body)}
}
