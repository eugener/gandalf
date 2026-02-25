package cloudauth

import (
	"errors"
	"net/http"
	"testing"

	"golang.org/x/oauth2"
)

// recordingTransport captures the last request for inspection.
type recordingTransport struct {
	lastReq *http.Request
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.lastReq = r
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func TestAPIKeyTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        string
		headerName string
		prefix     string
		wantHeader string
		wantValue  string
	}{
		{
			name:       "bearer auth",
			key:        "sk-test-123",
			headerName: "Authorization",
			prefix:     "Bearer ",
			wantHeader: "Authorization",
			wantValue:  "Bearer sk-test-123",
		},
		{
			name:       "api key header",
			key:        "key-456",
			headerName: "x-api-key",
			prefix:     "",
			wantHeader: "x-api-key",
			wantValue:  "key-456",
		},
		{
			name:       "azure api key",
			key:        "az-key",
			headerName: "api-key",
			prefix:     "",
			wantHeader: "api-key",
			wantValue:  "az-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &recordingTransport{}
			transport := &APIKeyTransport{
				Key:        tt.key,
				HeaderName: tt.headerName,
				Prefix:     tt.prefix,
				Base:       rec,
			}

			req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
			req.Header.Set("Content-Type", "application/json")

			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			resp.Body.Close()

			if got := rec.lastReq.Header.Get(tt.wantHeader); got != tt.wantValue {
				t.Errorf("header %q = %q, want %q", tt.wantHeader, got, tt.wantValue)
			}
			// Original request should not be modified.
			if got := req.Header.Get(tt.wantHeader); got != "" {
				t.Errorf("original request should not have %q header, got %q", tt.wantHeader, got)
			}
			// Content-Type should be preserved.
			if got := rec.lastReq.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
		})
	}
}

func TestAPIKeyTransportNilBase(t *testing.T) {
	t.Parallel()

	transport := &APIKeyTransport{
		Key:        "test",
		HeaderName: "Authorization",
		Prefix:     "Bearer ",
	}
	if transport.base() != http.DefaultTransport {
		t.Error("nil Base should fall back to http.DefaultTransport")
	}
}

// fakeTokenSource returns a fixed token or error.
type fakeTokenSource struct {
	token *oauth2.Token
	err   error
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	return f.token, f.err
}

func TestGCPOAuthTransport(t *testing.T) {
	t.Parallel()

	rec := &recordingTransport{}
	ts := &fakeTokenSource{token: &oauth2.Token{AccessToken: "ya29.test-token"}}
	transport := newGCPOAuthTransportFromSource(rec, ts)

	req, _ := http.NewRequest(http.MethodPost, "https://us-central1-aiplatform.googleapis.com/v1/...", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	if got := rec.lastReq.Header.Get("Authorization"); got != "Bearer ya29.test-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer ya29.test-token")
	}
	// Original request should not be modified.
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("original request should not have Authorization, got %q", got)
	}
}

func TestGCPOAuthTransportTokenError(t *testing.T) {
	t.Parallel()

	rec := &recordingTransport{}
	ts := &fakeTokenSource{err: errors.New("no credentials")}
	transport := newGCPOAuthTransportFromSource(rec, ts)

	req, _ := http.NewRequest(http.MethodPost, "https://example.com", nil)
	_, err := transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error when token source fails")
	}
}

func TestGCPOAuthTransportNilBase(t *testing.T) {
	t.Parallel()

	ts := &fakeTokenSource{token: &oauth2.Token{AccessToken: "test"}}
	transport := newGCPOAuthTransportFromSource(nil, ts)
	if transport.getBase() != http.DefaultTransport {
		t.Error("nil base should fall back to http.DefaultTransport")
	}
}
