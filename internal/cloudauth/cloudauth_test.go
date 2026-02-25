package cloudauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
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

// fakeAWSCredProvider returns fixed credentials or error.
type fakeAWSCredProvider struct {
	creds aws.Credentials
	err   error
}

func (f *fakeAWSCredProvider) Retrieve(_ context.Context) (aws.Credentials, error) {
	return f.creds, f.err
}

func TestAWSSigV4Transport(t *testing.T) {
	t.Parallel()

	rec := &recordingTransport{}
	creds := &fakeAWSCredProvider{
		creds: aws.Credentials{
			AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}
	transport := NewAWSSigV4Transport(rec, creds, "us-east-1", "bedrock-runtime")

	req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-3-5-sonnet/invoke",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	// Authorization header should start with AWS4-HMAC-SHA256.
	authHeader := rec.lastReq.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256") {
		t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 prefix", authHeader)
	}
	// X-Amz-Date should be present.
	if rec.lastReq.Header.Get("X-Amz-Date") == "" {
		t.Error("X-Amz-Date header missing")
	}
	// Original request should not have signing headers.
	if req.Header.Get("Authorization") != "" {
		t.Error("original request should not have Authorization header")
	}
}

func TestAWSSigV4TransportCredentialError(t *testing.T) {
	t.Parallel()

	rec := &recordingTransport{}
	creds := &fakeAWSCredProvider{err: errors.New("no credentials")}
	transport := NewAWSSigV4Transport(rec, creds, "us-east-1", "bedrock-runtime")

	req, _ := http.NewRequest(http.MethodPost, "https://example.com", strings.NewReader("body"))
	_, err := transport.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error when credentials fail")
	}
	if !strings.Contains(err.Error(), "no credentials") {
		t.Errorf("error = %q, want 'no credentials'", err)
	}
}

func TestAWSSigV4TransportEmptyBody(t *testing.T) {
	t.Parallel()

	rec := &recordingTransport{}
	creds := &fakeAWSCredProvider{
		creds: aws.Credentials{
			AccessKeyID:     "AKID",
			SecretAccessKey: "SECRET",
		},
	}
	transport := NewAWSSigV4Transport(rec, creds, "us-east-1", "bedrock-runtime")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip with nil body: %v", err)
	}
	resp.Body.Close()

	if rec.lastReq.Header.Get("Authorization") == "" {
		t.Error("expected Authorization header for nil body request")
	}
}

func TestAWSSigV4TransportNilBase(t *testing.T) {
	t.Parallel()

	creds := &fakeAWSCredProvider{
		creds: aws.Credentials{AccessKeyID: "AKID", SecretAccessKey: "SECRET"},
	}
	transport := NewAWSSigV4Transport(nil, creds, "us-east-1", "bedrock-runtime")
	if transport.getBase() != http.DefaultTransport {
		t.Error("nil base should fall back to http.DefaultTransport")
	}
}
