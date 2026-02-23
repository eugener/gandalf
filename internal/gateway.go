// Package gateway defines domain types and interfaces for the Gandalf LLM gateway.
// This package has no project imports -- it is the dependency root.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// --- Provider ---

// Provider is the interface that all LLM provider adapters must implement.
type Provider interface {
	// Name returns the provider identifier (e.g., "openai", "anthropic").
	Name() string
	// ChatCompletion sends a non-streaming chat completion request.
	ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	// ChatCompletionStream sends a streaming chat completion request.
	ChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
	// Embeddings generates embeddings for input text.
	Embeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
	// ListModels returns the list of available model IDs.
	ListModels(ctx context.Context) ([]string, error)
	// HealthCheck verifies connectivity to the provider.
	HealthCheck(ctx context.Context) error
}

// ChatRequest represents an OpenAI-compatible chat completion request.
type ChatRequest struct {
	Model            string          `json:"model"`
	Messages         []Message       `json:"messages"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	N                int             `json:"n,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	StreamOptions    *StreamOptions  `json:"stream_options,omitempty"`
	Stop             json.RawMessage `json:"stop,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	User             string          `json:"user,omitempty"`
	Tools            json.RawMessage `json:"tools,omitempty"`
	ToolChoice       json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat   json.RawMessage `json:"response_format,omitempty"`
}

// StreamOptions controls streaming behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// Message represents a chat message.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// ChatResponse represents an OpenAI-compatible chat completion response.
type ChatResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// Choice represents a single completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage represents token usage statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk represents a single chunk in a streaming response.
type StreamChunk struct {
	Data  []byte // raw SSE data line, forwarded as-is when possible
	Usage *Usage // non-nil on final chunk
	Done  bool
	Err   error
}

// EmbeddingRequest represents an OpenAI-compatible embedding request.
type EmbeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format,omitempty"`
	User           string          `json:"user,omitempty"`
}

// EmbeddingResponse represents an OpenAI-compatible embedding response.
type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   json.RawMessage `json:"data"`
	Model  string          `json:"model"`
	Usage  *Usage          `json:"usage,omitempty"`
}

// --- Multi-tenant identity ---

// Organization represents a top-level tenant.
type Organization struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	AllowedModels []string `json:"allowed_models,omitempty"` // nil = all models
	RPMLimit      *int64   `json:"rpm_limit,omitempty"`
	TPMLimit      *int64   `json:"tpm_limit,omitempty"`
	MaxBudget     *float64 `json:"max_budget,omitempty"` // USD
	CreatedAt     time.Time `json:"created_at"`
}

// Team is a subdivision within an organization.
type Team struct {
	ID            string   `json:"id"`
	OrgID         string   `json:"org_id"`
	Name          string   `json:"name"`
	AllowedModels []string `json:"allowed_models,omitempty"` // nil = inherit from org
	RPMLimit      *int64   `json:"rpm_limit,omitempty"`
	TPMLimit      *int64   `json:"tpm_limit,omitempty"`
	MaxBudget     *float64 `json:"max_budget,omitempty"`
}

// APIKey represents an API key for authentication.
type APIKey struct {
	ID            string     `json:"id"`
	KeyHash       string     `json:"-"`                        // SHA-256 hex, never exposed
	KeyPrefix     string     `json:"key_prefix"`               // first 8 chars for display
	UserID        string     `json:"user_id,omitempty"`
	TeamID        string     `json:"team_id,omitempty"`
	OrgID         string     `json:"org_id"`
	AllowedModels []string   `json:"allowed_models,omitempty"` // nil = inherit from team
	RPMLimit      *int64     `json:"rpm_limit,omitempty"`
	TPMLimit      *int64     `json:"tpm_limit,omitempty"`
	MaxBudget     *float64   `json:"max_budget,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	Blocked       bool       `json:"blocked"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Identity is the authenticated caller context attached to request context.
// Populated by either JWT or API key auth.
type Identity struct {
	Subject    string     `json:"subject"`     // JWT sub or key prefix
	KeyID      string     `json:"key_id"`      // API key ID for per-key bucketing
	UserID     string     `json:"user_id"`
	TeamID     string     `json:"team_id"`
	OrgID      string     `json:"org_id"`
	Role       string     `json:"role"`        // "admin", "member", "viewer", "service_account"
	Perms      Permission `json:"-"`           // resolved bitmask
	AuthMethod string     `json:"auth_method"` // "jwt" or "apikey"
	RPMLimit   int64      `json:"-"`           // effective RPM limit (0 = unlimited)
	TPMLimit   int64      `json:"-"`           // effective TPM limit (0 = unlimited)
	MaxBudget  float64    `json:"-"`           // max spend USD (0 = unlimited)
}

// --- RBAC ---

// Permission is a bitmask representing authorization capabilities.
type Permission uint32

const (
	PermUseModels       Permission = 1 << iota // call /v1/chat/completions, /v1/embeddings
	PermManageOwnKeys                          // create/delete own API keys
	PermViewOwnUsage                           // view own usage stats
	PermViewAllUsage                           // view org-wide usage
	PermManageAllKeys                          // manage any key in the org
	PermManageProviders                        // configure upstream providers
	PermManageRoutes                           // configure model routing
	PermManageOrgs                             // manage orgs and teams
)

// Can reports whether the identity has the given permission.
func (id *Identity) Can(p Permission) bool { return id.Perms&p == p }

// RolePermissions maps role names to their permission bitmasks.
var RolePermissions = map[string]Permission{
	"admin":           PermUseModels | PermManageOwnKeys | PermViewOwnUsage | PermViewAllUsage | PermManageAllKeys | PermManageProviders | PermManageRoutes | PermManageOrgs,
	"member":          PermUseModels | PermManageOwnKeys | PermViewOwnUsage,
	"viewer":          PermViewOwnUsage | PermViewAllUsage,
	"service_account": PermUseModels,
}

// --- Provider config (stored in DB) ---

// ProviderConfig represents a configured upstream LLM provider.
type ProviderConfig struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	BaseURL   string   `json:"base_url"`
	APIKeyEnc string   `json:"-"`           // deprecated: no longer persisted, kept for schema compat
	Models    []string `json:"models"`
	Priority  int      `json:"priority"`
	Weight    int      `json:"weight"`
	Enabled   bool     `json:"enabled"`
	MaxRPS    int      `json:"max_rps"`
	TimeoutMs int      `json:"timeout_ms"`
}

// Route maps a model alias to provider targets.
type Route struct {
	ID         string          `json:"id"`
	ModelAlias string          `json:"model_alias"`
	Targets    json.RawMessage `json:"targets"` // []RouteTarget as JSON
	Strategy   string          `json:"strategy"`
	CacheTTLs  int             `json:"cache_ttl_s"`
}

// RouteTarget is a single target within a route.
type RouteTarget struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Priority   int    `json:"priority"`
	Weight     int    `json:"weight"`
}

// UsageRecord represents a single API usage event.
type UsageRecord struct {
	ID               string    `json:"id"`
	KeyID            string    `json:"key_id"`
	UserID           string    `json:"user_id,omitempty"`
	TeamID           string    `json:"team_id,omitempty"`
	OrgID            string    `json:"org_id"`
	CallerJWTSub     string    `json:"caller_jwt_sub,omitempty"`
	CallerService    string    `json:"caller_service,omitempty"`
	Model            string    `json:"model"`
	ProviderID       string    `json:"provider_id"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	CostUSD          float64   `json:"cost_usd,omitempty"`
	Cached           bool      `json:"cached"`
	LatencyMs        int       `json:"latency_ms"`
	StatusCode       int       `json:"status_code"`
	RequestID        string    `json:"request_id"`
	CreatedAt        time.Time `json:"created_at"`
}

// --- Context keys ---

type contextKey int

const ctxKeyMeta contextKey = 0

// requestMeta bundles per-request values into a single context allocation.
// The Identity field is set later by the authenticate middleware via mutation
// of the same pointer, avoiding a second context.WithValue + Request.WithContext.
type requestMeta struct {
	RequestID string
	Identity  *Identity
}

// metaFromContext returns the requestMeta stored in ctx, or nil.
func metaFromContext(ctx context.Context) *requestMeta {
	m, _ := ctx.Value(ctxKeyMeta).(*requestMeta)
	return m
}

// IdentityFromContext extracts the authenticated identity from context.
func IdentityFromContext(ctx context.Context) *Identity {
	if m := metaFromContext(ctx); m != nil {
		return m.Identity
	}
	return nil
}

// ContextWithIdentity stores the identity in the existing requestMeta if present,
// avoiding a new context.WithValue allocation. Falls back to creating new metadata
// if none exists (e.g., in tests).
func ContextWithIdentity(ctx context.Context, id *Identity) context.Context {
	if m := metaFromContext(ctx); m != nil {
		m.Identity = id
		return ctx
	}
	return context.WithValue(ctx, ctxKeyMeta, &requestMeta{Identity: id})
}

// RequestIDFromContext extracts the request ID from context.
func RequestIDFromContext(ctx context.Context) string {
	if m := metaFromContext(ctx); m != nil {
		return m.RequestID
	}
	return ""
}

// ContextWithRequestID returns a context carrying the given request ID.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyMeta, &requestMeta{RequestID: id})
}

// --- Native passthrough ---

// NativeProxy is an optional interface that providers can implement to support
// raw HTTP passthrough. The gateway authenticates and routes the request, then
// delegates the raw HTTP exchange to the provider. Checked via type assertion.
type NativeProxy interface {
	// ProxyRequest forwards a raw HTTP request to the provider's API.
	// path is the provider-relative path (e.g. "/messages").
	// The implementation handles auth headers, URL construction, and
	// response streaming (flush-on-read for SSE/NDJSON).
	ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error
}

// --- Shared constants and helpers ---

// APIKeyPrefix is the prefix for all Gandalf API keys.
const APIKeyPrefix = "gnd_"

// HashKey returns the hex-encoded SHA-256 hash of a raw API key.
func HashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// --- Authenticator interface ---

// Authenticator validates request credentials and returns the caller identity.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (*Identity, error)
}
