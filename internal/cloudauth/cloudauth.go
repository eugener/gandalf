// Package cloudauth provides http.RoundTripper decorators that inject
// authentication headers for cloud-hosted LLM providers (direct API keys,
// GCP OAuth, Azure Entra).
package cloudauth

import "net/http"

// APIKeyTransport is an http.RoundTripper that injects a static API key
// header on every outbound request. HeaderName is the header to set
// (e.g. "Authorization", "x-api-key"). Prefix is prepended to Key
// (e.g. "Bearer " for Authorization headers).
type APIKeyTransport struct {
	Key        string
	HeaderName string
	Prefix     string
	Base       http.RoundTripper
}

// RoundTrip clones the request and sets the auth header.
func (t *APIKeyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.Header.Set(t.HeaderName, t.Prefix+t.Key)
	return t.base().RoundTrip(r2)
}

func (t *APIKeyTransport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}
