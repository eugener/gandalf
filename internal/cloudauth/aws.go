package cloudauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// AWSSigV4Transport is an http.RoundTripper that signs outbound requests
// with AWS Signature Version 4. It buffers the request body to compute
// the SHA-256 payload hash required by SigV4.
type AWSSigV4Transport struct {
	base    http.RoundTripper
	creds   aws.CredentialsProvider
	signer  *v4.Signer
	region  string
	service string
}

// NewAWSSigV4Transport returns a transport that signs requests using AWS SigV4.
// region and service identify the target (e.g. "us-east-1", "bedrock-runtime").
func NewAWSSigV4Transport(base http.RoundTripper, creds aws.CredentialsProvider, region, service string) *AWSSigV4Transport {
	return &AWSSigV4Transport{
		base:    base,
		creds:   creds,
		signer:  v4.NewSigner(),
		region:  region,
		service: service,
	}
}

// RoundTrip buffers the body for SHA-256 hashing, retrieves credentials,
// signs the request, and forwards it to the base transport.
func (t *AWSSigV4Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Buffer body for payload hash.
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("cloudauth: read body for signing: %w", err)
		}
		r.Body.Close()
	}

	payloadHash := sha256Hex(bodyBytes)

	// Clone request to avoid mutating the original.
	r2 := r.Clone(r.Context())
	if len(bodyBytes) > 0 {
		r2.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		r2.ContentLength = int64(len(bodyBytes))
	} else {
		r2.Body = http.NoBody
		r2.ContentLength = 0
	}

	creds, err := t.creds.Retrieve(r.Context())
	if err != nil {
		return nil, fmt.Errorf("cloudauth: retrieve AWS credentials: %w", err)
	}

	if err := t.signer.SignHTTP(r.Context(), creds, r2, payloadHash, t.service, t.region, time.Now()); err != nil {
		return nil, fmt.Errorf("cloudauth: sign request: %w", err)
	}

	return t.getBase().RoundTrip(r2)
}

func (t *AWSSigV4Transport) getBase() http.RoundTripper {
	if t.base != nil {
		return t.base
	}
	return http.DefaultTransport
}

// sha256Hex returns the hex-encoded SHA-256 hash of data.
// Returns the hash of an empty string for nil/empty input.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
