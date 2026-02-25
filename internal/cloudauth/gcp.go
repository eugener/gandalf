package cloudauth

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GCPOAuthTransport is an http.RoundTripper that injects a GCP OAuth2
// bearer token on every outbound request, using Application Default
// Credentials (ADC). Tokens are cached and auto-refreshed.
type GCPOAuthTransport struct {
	base   http.RoundTripper
	source oauth2.TokenSource
}

// NewGCPOAuthTransport returns a transport that obtains GCP credentials
// via ADC and injects an Authorization: Bearer header on each request.
// scopes specifies the required OAuth2 scopes.
func NewGCPOAuthTransport(ctx context.Context, base http.RoundTripper, scopes ...string) (*GCPOAuthTransport, error) {
	creds, err := google.FindDefaultCredentials(ctx, scopes...)
	if err != nil {
		return nil, fmt.Errorf("cloudauth: find GCP credentials: %w", err)
	}
	return &GCPOAuthTransport{
		base:   base,
		source: oauth2.ReuseTokenSource(nil, creds.TokenSource),
	}, nil
}

// newGCPOAuthTransportFromSource creates a GCPOAuthTransport with an
// explicit token source (used for testing).
func newGCPOAuthTransportFromSource(base http.RoundTripper, ts oauth2.TokenSource) *GCPOAuthTransport {
	return &GCPOAuthTransport{
		base:   base,
		source: oauth2.ReuseTokenSource(nil, ts),
	}
}

// RoundTrip obtains a token and injects it as a Bearer header.
func (t *GCPOAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tok, err := t.source.Token()
	if err != nil {
		return nil, fmt.Errorf("cloudauth: obtain GCP token: %w", err)
	}
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	return t.getBase().RoundTrip(r2)
}

func (t *GCPOAuthTransport) getBase() http.RoundTripper {
	if t.base != nil {
		return t.base
	}
	return http.DefaultTransport
}
