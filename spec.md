# Gandalf -- High-Performance LLM Gateway (Go)

## Context

Build a production-grade LLM gateway that unifies multiple providers (OpenAI, Anthropic, Gemini) behind an OpenAI-compatible API. Primary design goal: **minimize latency** on the hot path. Single-binary deployment with SQLite, full observability stack.

---

## Architecture

```
Clients (OpenAI SDK, Claude CLI, Gemini CLI, Azure SDK, Ollama tools, curl)
         |
         v
+------- Gandalf Gateway ---------------------------------------------------+
|  Middleware Chain (zero-alloc hot path)                                    |
|  [Recovery] -> [RequestID] -> [OTel Span] -> [Metrics]                    |
|  -> [Logger] -> [NormalizeAuth] -> [Auth] -> [RateLimit] -> [Cache]       |
|         |                                                                 |
|  Router (chi)                                                             |
|  Universal: /v1/chat/completions, /v1/embeddings, /v1/models              |
|  Native:    /v1/messages, /v1beta/models/*, /openai/deployments/*,        |
|             /api/chat, /api/embed, /api/tags                              |
|  Admin:     /admin/v1/*                                                   |
|  System:    /healthz, /readyz, /metrics                                   |
|         |                                                                 |
|  Universal: Proxy Handler -> RouterService -> Provider Adapters           |
|  Native:    NativeProxy passthrough (raw HTTP forwarding, zero xlate)     |
|             [OpenAI | Anthropic | Gemini | Ollama]                        |
|         |                                                                 |
|  SQLite (config, keys, usage)    In-Memory Cache (W-TinyLFU)              |
+----------------------------------------------------------------------- --+
         |
         v
OpenAI API / Anthropic API / Gemini API / Ollama (local)
```

## Latency Minimization Strategy

These are non-negotiable design constraints:

1. **Zero-copy SSE streaming** -- read provider chunks directly into `ResponseWriter`, no intermediate buffering or JSON re-parsing unless format translation is needed
2. **Pre-warmed HTTP connection pools** -- persistent `http.Client` per provider with keep-alive, `MaxIdleConnsPerHost` tuned to expected concurrency
3. **In-memory rate limiter** -- token bucket in memory, sub-microsecond checks. DB sync is async (every 10s), never on hot path
4. **Async usage recording** -- usage records go to a buffered channel, flushed to DB in batches by background goroutine. Never blocks the response
5. **Auth cache** -- W-TinyLFU cache (otter) for API key hash -> identity (30s TTL) and JWKS auto-refresh cache (15min) for JWT validation. No DB or IdP call on hot path
6. **Response cache before provider call** -- cache middleware short-circuits before any provider adapter is invoked
7. **Lazy token counting** -- count tokens in a separate goroutine after response is sent (for non-streaming). For streaming, count incrementally per chunk without blocking the write
8. **Minimal allocations in middleware** -- use `sync.Pool` for request-scoped buffers, avoid `fmt.Sprintf` on hot path
9. **`Transfer-Encoding: chunked` passthrough** -- for streaming, set headers and flush immediately, don't wait for first chunk
10. **DNS caching** -- `rs/dnscache` resolver wrapped in `DialContext` to avoid DNS lookup per new connection

## Directory Structure

```
gandalf/
  cmd/gandalf/
    main.go                        # Entrypoint: parse flags, call run()
    run.go                         # Wire deps, start server + workers, graceful shutdown
  internal/
    gateway.go                     # Domain types, Authenticator interface, RBAC bitmask,
                                   #   context helpers. No project imports.
    errors.go                      # Domain error types + sentinel errors
    auth/
      apikey.go                    # API key auth: hash -> cache lookup -> DB fallback
      jwt.go                       # (Phase 5) JWT/OIDC auth: JWKS cache, token validation
      auth.go                      # (Phase 5) Dual-mode dispatcher: JWT first, API key fallback
      auth_test.go
    server/
      server.go                    # NewServer(deps) http.Handler -- Mat Ryer pattern
      routes.go                    # Single file: all route registrations
      proxy.go                     # handleChatCompletion, handleEmbeddings handlers
      models.go                    # handleListModels handler
      admin.go                     # handleAdmin* CRUD handlers
      health.go                    # handleHealthz, handleReadyz
      middleware.go                # Rate limit, cache, logging, metrics, tracing,
                                   #   request ID, recovery -- middleware factories
      server_test.go               # Handler + middleware tests via httptest.NewRecorder
    app/
      proxy.go                     # ProxyService: route -> cache check -> provider call -> record
      proxy_test.go
      keymanager.go                # API key CRUD + validation
      keymanager_test.go
      orgmanager.go                # Organization/Team CRUD, limit inheritance
      orgmanager_test.go
      router.go                    # Model-alias -> provider target resolution
      router_test.go
      usage.go                     # UsageService: quota checks, query, aggregation
      usage_test.go
    provider/
      provider.go                  # Provider interface + StreamChunk, ChatRequest/Response types
      registry.go                  # ProviderRegistry: name -> Provider, explicit registration
      openai/
        client.go                  # OpenAI adapter (reference implementation)
        client_test.go
      anthropic/
        client.go                  # Anthropic adapter (message format translation)
        client_test.go
      gemini/
        client.go                  # Gemini adapter
        client_test.go
    storage/
      storage.go                   # Store interfaces: APIKeyStore, ProviderStore, RouteStore,
                                   #   UsageStore, OrgStore, TeamStore
      sqlite/
        db.go                      # New(dsn) -- open DB, run migrations, return Store
        migrations/                # Embedded SQL migration files (goose)
          001_init.sql
        apikey.go                  # APIKeyStore implementation
        provider.go                # ProviderStore implementation
        route.go                   # RouteStore implementation
        usage.go                   # UsageStore implementation
        org.go                     # OrgStore + TeamStore implementation
        sqlite_test.go             # Integration tests against :memory: SQLite
    cache/
      cache.go                     # Cache interface (Get/Set/Delete/Purge)
      memory.go                    # In-memory W-TinyLFU cache (otter)
      memory_test.go
    worker/
      worker.go                    # Worker interface: Run(ctx) error
      runner.go                    # errgroup-based runner, starts all workers
      usage_recorder.go            # Buffered channel -> batch DB insert
      ratelimit_sync.go            # In-memory rate limit state -> DB sync
    config/
      config.go                    # Config struct, Load(path), env var expansion
      bootstrap.go                 # Seed DB from YAML on first run
      config_test.go
    telemetry/
      telemetry.go                 # Setup(cfg) (shutdown func, error) -- single init point
      metrics.go                   # Prometheus metric definitions, pre-cached label children
      tracing.go                   # OTel tracer provider + OTLP exporter setup
      logging.go                   # zerolog setup, diode writer
    testutil/                      # Shared test helpers (NOT production code)
      fake_provider.go             # FakeProvider implements provider.Provider
      fake_store.go                # In-memory store fakes for unit tests
      server_helper.go             # newTestServer(t, opts...) helper
      roundtrip.go                 # Custom http.RoundTripper for HTTP mocking
  configs/gandalf.yaml             # Example bootstrap config
  testdata/
    cassettes/                     # go-vcr recorded HTTP responses for provider tests
  deploy/
    Dockerfile                     # Multi-stage, CGO_ENABLED=0
    docker-compose.yml             # Dev stack (gandalf + prometheus + jaeger)
  Makefile
```

### Structure rationale

**Domain types at `internal/gateway.go`** (Ben Johnson pattern) -- interfaces and types live at the root of `internal/`, imported by everything, importing nothing from the project. Eliminates the `domain/` + `port/` split that creates artificial package boundaries.

**`auth/` = authentication and authorization** -- dual-mode dispatcher (JWT first, API key fallback). JWKS cache for JWT validation, otter cache for API key lookups. RBAC via permission bitmask (no DB lookup per request). Separated from `server/` because auth logic is reusable beyond HTTP (e.g., gRPC in the future).

**`server/` = HTTP transport layer** (Mat Ryer pattern) -- `NewServer(deps) http.Handler` takes all dependencies via constructor, returns a testable handler. Single `routes.go` file maps the entire API surface. Handler makers return closures: initialization runs once, not per-request. Middleware factories live here since they're HTTP-specific.

**`app/` = application services** (go-kit endpoint layer) -- pure business logic, no HTTP awareness. Depends on domain interfaces from `internal/gateway.go`. Testable with fakes, no HTTP server needed. Includes org/team management with limit inheritance.

**`provider/` = LLM adapter layer** -- each adapter is its own sub-package for isolation. `provider.go` defines the `Provider` interface consumed by `app/`. Registry enables explicit registration in `cmd/` (no hidden `init()` coupling).

**`storage/` = persistence layer** -- interfaces in `storage.go`, implementations in sub-packages (`sqlite/`, future `postgres/`). SQLite tests use `:memory:` -- same engine, no containers needed.

**`worker/` = background goroutines** -- each worker implements `Worker` interface with `Run(ctx) error`. Runner uses `errgroup`. Clean shutdown via context cancellation.

**`testutil/` = shared test fakes** -- importable by any `_test.go` file. Hand-written fakes over generated mocks for small interfaces (compile-time interface checks, inspectable state, no EXPECT boilerplate).

**`testdata/cassettes/`** -- go-vcr recorded HTTP responses for provider adapter tests. Committed to VCS, deterministic replay in CI without network access.

### Dependency flow

```
cmd/gandalf         -- wires concrete types, imports everything
  -> server/        -- HTTP transport, depends on app interfaces
  -> app/           -- business logic, depends on domain interfaces
  -> internal/      -- domain types + interfaces (no project imports)
  <- provider/*     -- implements gateway.Provider
  <- storage/*      -- implements gateway.*Store
  <- cache/         -- implements gateway.Cache
  <- worker/        -- implements background tasks
  <- telemetry/     -- OTel SDK wiring, only imported by cmd/
  <- config/        -- Config struct + Load()
```

Arrows show import direction. No circular dependencies because interfaces flow inward (defined at consumer, implemented by adapters).

### Testing strategy

| Layer | Test type | Approach |
|-------|-----------|----------|
| `server/` | Unit | `httptest.NewRecorder` + chi router's `ServeHTTP`. Fakes for app services |
| `server/` (SSE) | Integration | `httptest.NewServer` + streaming client. Needs real TCP for `Flush()` |
| `app/` | Unit | Table-driven tests with `testutil.FakeProvider` and `testutil.FakeStore` |
| `provider/*` | Integration | go-vcr cassettes for deterministic replay. `t.Skip` when no env var |
| `storage/sqlite/` | Integration | Real `:memory:` SQLite. Same engine, no containers |
| `middleware` | Unit + bench | Wrap known handler, assert status/headers. `b.ReportAllocs()` for hot path |
| `worker/` | Unit | Inject fake store, verify batching/flushing behavior |
| End-to-end | Integration | `httptest.NewServer(app.Router)` + fake upstream `httptest.NewServer` |

Integration tests gated by `t.Skip` + env vars (not build tags) -- keeps files compiled, catches typos in CI.

## Key Interfaces

Interfaces are defined at the consumer (domain) level, not alongside implementations. This prevents circular imports and enables clean dependency injection.

```go
// internal/gateway.go -- domain types, no project imports
package gateway

// --- Provider ---

type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    ChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
    Embeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
    ListModels(ctx context.Context) ([]string, error)
    HealthCheck(ctx context.Context) error
}

type StreamChunk struct {
    Data  []byte  // raw SSE data line, forwarded as-is when possible
    Usage *Usage  // non-nil on final chunk
    Done  bool
    Err   error
}

// Optional interface for native API passthrough (checked via type assertion).
// Providers implement this to support raw HTTP forwarding without translation.
type NativeProxy interface {
    ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error
}

// --- Multi-tenant identity ---

type Organization struct {
    ID            string
    Name          string
    AllowedModels []string  // nil = all models
    RPMLimit      *int64    // nil = default
    TPMLimit      *int64
    MaxBudget     *float64  // USD, nil = unlimited
}

type Team struct {
    ID            string
    OrgID         string
    Name          string
    AllowedModels []string  // nil = inherit from org
    RPMLimit      *int64    // nil = inherit from org
    TPMLimit      *int64
    MaxBudget     *float64
}

type APIKey struct {
    ID            string
    KeyHash       string    // SHA-256 hex, indexed
    KeyPrefix     string    // first 8 chars for display: "gnd_abc1..."
    UserID        string
    TeamID        string
    OrgID         string
    AllowedModels []string  // nil = inherit from team
    RPMLimit      *int64    // nil = inherit from team
    TPMLimit      *int64
    MaxBudget     *float64
    ExpiresAt     *time.Time
    Blocked       bool
}

// Identity is the authenticated caller context, attached to request context.
// Populated by either JWT or API key auth.
type Identity struct {
    Subject   string      // JWT sub or key prefix
    UserID    string
    TeamID    string
    OrgID     string
    Role      string      // "admin", "member", "viewer", "service_account"
    Perms     Permission  // resolved bitmask
    AuthMethod string     // "jwt" or "apikey"
}

// --- RBAC ---

type Permission uint32

const (
    PermUseModels      Permission = 1 << iota  // call /v1/chat/completions, /v1/embeddings
    PermManageOwnKeys                           // create/delete own API keys
    PermViewOwnUsage                            // view own usage stats
    PermViewAllUsage                            // view org-wide usage
    PermManageAllKeys                           // manage any key in the org
    PermManageProviders                         // configure upstream providers
    PermManageRoutes                            // configure model routing
    PermManageOrgs                              // manage orgs and teams
)
```

```go
// internal/gateway.go -- authenticator interface (at domain root, no project imports)
type Authenticator interface {
    // Authenticate extracts and validates credentials from the request.
    // Returns Identity on success. Tries JWT first, falls back to API key.
    Authenticate(ctx context.Context, r *http.Request) (*Identity, error)
}
```

```go
// internal/storage/storage.go -- store interfaces, consumed by app/
type APIKeyStore interface {
    CreateKey(ctx context.Context, key *gateway.APIKey) error
    GetKeyByHash(ctx context.Context, hash string) (*gateway.APIKey, error)
    ListKeys(ctx context.Context, orgID string, offset, limit int) ([]*gateway.APIKey, error)
    UpdateKey(ctx context.Context, key *gateway.APIKey) error
    DeleteKey(ctx context.Context, id string) error
    TouchKeyUsed(ctx context.Context, id string) error
}

type OrgStore interface {
    CreateOrg(ctx context.Context, org *gateway.Organization) error
    GetOrg(ctx context.Context, id string) (*gateway.Organization, error)
    ListOrgs(ctx context.Context, offset, limit int) ([]*gateway.Organization, error)
    UpdateOrg(ctx context.Context, org *gateway.Organization) error
    DeleteOrg(ctx context.Context, id string) error
    CreateTeam(ctx context.Context, team *gateway.Team) error
    GetTeam(ctx context.Context, id string) (*gateway.Team, error)
    ListTeams(ctx context.Context, orgID string, offset, limit int) ([]*gateway.Team, error)
    UpdateTeam(ctx context.Context, team *gateway.Team) error
    DeleteTeam(ctx context.Context, id string) error
}

// ProviderStore, RouteStore, UsageStore follow same CRUD pattern
```

```go
// internal/cache/cache.go -- cache interface
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, bool)
    Set(ctx context.Context, key string, val []byte, ttl time.Duration)
    Delete(ctx context.Context, key string)
    Purge(ctx context.Context)
}
```

```go
// internal/worker/worker.go -- background worker interface
type Worker interface {
    Run(ctx context.Context) error
}
```

## Authentication and Authorization

### Auth Architecture

Dual-mode authentication: JWT/OIDC for enterprise SSO integration, API keys for programmatic access. Both resolve to the same `Identity` type for downstream processing.

```
Request arrives
  |
  +-- Authorization: Bearer eyJhbG...  (JWT)
  |     -> Validate signature against cached JWKS (no IdP call)
  |     -> Extract org_id, team_id, role from claims
  |     -> Auto-upsert user on first JWT auth
  |     -> Return Identity
  |
  +-- Authorization: Bearer gnd_...    (API key)
  |     -> SHA-256 hash -> otter cache lookup (30s TTL)
  |     -> Cache miss: DB lookup by hash
  |     -> Resolve org/team limits via inheritance
  |     -> Return Identity
  |
  +-- Neither -> 401 Unauthorized
```

### JWT/OIDC Validation

Library: `github.com/lestrrat-go/jwx/v2` -- full JOSE stack with background JWKS auto-refresh.

- JWKS cache with 15-minute refresh interval, serves stale on refresh failure
- On unknown `kid`: force-refresh JWKS and retry once (handles key rotation)
- Validate: signature, `exp`, `iss`, `aud`
- Extract identity fields via configurable claim paths (supports dot notation, e.g., `resource_access.client.roles`)
- Support multiple JWKS URLs for multi-IdP scenarios

### RBAC

Four roles, mapped to permission bitmask:

| Role | Permissions |
|------|-------------|
| `admin` | All permissions |
| `member` | Use models, manage own keys, view own usage |
| `viewer` | View own and org-wide usage |
| `service_account` | Use models only |

Permission check is a single bitwise AND -- no DB lookup, no map access on hot path:

```go
func (id *Identity) Can(p Permission) bool { return id.Perms&p == p }
```

### Multi-Tenant Limit Inheritance

Limits resolve bottom-up: key -> team -> org -> global default. First non-nil wins.

```
Organization (acme)         rpm=1000, tpm=100000, models=[gpt-4o, claude-*]
  Team (backend)            rpm=nil(inherit), tpm=50000, models=nil(inherit)
    API Key (gnd_abc...)    rpm=100, tpm=nil(inherit)
    -> Effective: rpm=100, tpm=50000, models=[gpt-4o, claude-*]
```

### Phased Auth Rollout

**Phase 1 (MVP)**: API key auth only. Keys scoped to org with `allowed_models` and rate limits.

**Phase 2 (Enterprise)**: Add JWT/OIDC. Dual-mode middleware. Multi-tenant org/team hierarchy. RBAC with bitmask permissions. Admin endpoints for org/team CRUD.

**Phase 3 (Full Enterprise)**: SSO/SAML via Dex as companion service (gateway only validates Dex-issued JWTs, no SAML code in gateway). mTLS for zero-trust environments (`tls.RequireAndVerifyClientCert`, extract identity from cert CN/SAN). OPA integration for complex policies (embed as library, cache decisions 30s).

## Database Schema (SQLite, Postgres-compatible)

- **organizations** -- id, name, allowed_models (JSON), rpm_limit, tpm_limit, max_budget, created_at
- **teams** -- id, org_id (FK), name, allowed_models (JSON), rpm_limit, tpm_limit, max_budget
- **providers** -- id, name, base_url, api_key_enc, models (JSON), priority, weight, enabled, max_rps, timeout_ms
- **api_keys** -- id, key_hash (SHA-256, indexed), key_prefix, user_id, team_id (FK), org_id (FK), allowed_models (JSON), rpm_limit, tpm_limit, max_budget, expires_at, blocked, last_used_at, metadata (JSON)
- **routes** -- id, model_alias (unique), targets (JSON), strategy, cache_ttl_s
- **usage_records** -- id, key_id, user_id, team_id, org_id, caller_jwt_sub, caller_service, model, provider_id, prompt_tokens, completion_tokens, total_tokens, cost_usd, cached, latency_ms, status_code, request_id, created_at (append-only, indexed by key_id+created_at)
- **usage_rollups** -- pre-aggregated hourly/daily summaries (background job)

## API Surface

**Client-facing -- Universal API (OpenAI-compatible, translated):**
- `POST /v1/chat/completions` -- streaming and non-streaming
- `POST /v1/embeddings`
- `GET /v1/models`

**Client-facing -- Native API Passthrough (raw forwarding, zero translation):**
- `POST /v1/messages` -- Anthropic Messages API
- `POST /v1beta/models/{model}:generateContent` -- Gemini
- `POST /v1beta/models/{model}:streamGenerateContent` -- Gemini streaming
- `POST /v1beta/models/{model}:embedContent` -- Gemini embeddings
- `GET /v1beta/models` -- Gemini list models
- `POST /openai/deployments/{dep}/chat/completions` -- Azure OpenAI
- `POST /openai/deployments/{dep}/embeddings` -- Azure OpenAI embeddings
- `POST /api/chat` -- Ollama native chat
- `POST /api/embed` -- Ollama native embeddings
- `GET /api/tags` -- Ollama list models

**Admin (requires admin role):**
- `/admin/v1/providers` -- CRUD
- `/admin/v1/keys` -- CRUD (full key returned only on create)
- `/admin/v1/routes` -- CRUD
- `/admin/v1/organizations` -- CRUD
- `/admin/v1/teams` -- CRUD
- `/admin/v1/usage` -- query + summary
- `POST /admin/v1/cache/purge`
- `POST /admin/v1/auth/configure` -- set JWKS URL, issuer, audience, claim mappings

**System:**
- `GET /healthz`, `GET /readyz`, `GET /metrics`

## Request Flow (Hot Path)

```
1. RequestID middleware    (~0.1us)  -- UUID v7 generation
2. OTel span start        (~0.5us)  -- span context injection
3. Prometheus timer start  (~0.1us)
4. Auth: JWT JWKS cache or   (~0.3us)  -- JWT: validate against cached JWKS (no IdP call)
          otter key cache              -- API key: hash -> otter cache (DB only on miss)
5. Rate limit: bucket check(~0.1us)  -- pure in-memory
6. Cache: hash + otter get  (~0.3us)  -- short-circuit on hit
7. Route resolve           (~0.3us)  -- cached route table
8. Provider HTTP call      (~200ms-30s) -- THE bottleneck
9. Response write          (~0.1us for non-stream)
10. Async: usage record    (0us on hot path -- buffered channel)
```

Total gateway overhead target: **< 5us** excluding provider call.

## Algorithms and Implementation Details

### Provider Selection: Peak EWMA + P2C

Select upstream provider using **Peak Exponentially Weighted Moving Average** with **Power of Two Choices**. Score on TTFT (time to first token), not total request duration -- a streaming request that received its first token is "done" from the load balancer's perspective.

```
score = ewma_latency * (outstanding_requests + 1)
ewma  = alpha * sample + (1 - alpha) * ewma_prev    // alpha = 0.3

SelectProvider(providers):
  tier = filter providers by health score > 0.8
  a, b = random_sample(tier, 2)
  return a if a.score <= b.score else b
```

For streaming: increment `OutstandingReqs` on stream start, decrement on first token received (not stream close). This reflects that TTFT is the latency metric that matters for routing.

Priority tiers (primary / fallback / last-resort) handle hard failures without waiting for EWMA to converge.

### Circuit Breaker

Per-model with provider-level rollup. Time-based sliding window (60s, 1-second buckets).

```
States: CLOSED -> (error_rate > threshold) -> OPEN -> (timeout) -> HALF_OPEN -> probe

Model CB threshold:    15% error rate (min 10 samples)
Provider CB threshold: 30% error rate
```

Failure classification with weighted impact on error rate:
- **429** (rate limited): weight 0.5 -- provider alive, just busy
- **500/502** (server error): weight 1.0
- **Timeout**: weight 1.5 -- worst signal, held connection and got nothing
- **400/401/404** (client error): weight 0.0 -- not a provider health signal

### Retry Strategy

**Full jitter exponential backoff** (AWS recommendation), prevents retry storms when many clients hit the same failure:

```
delay = random_between(0, min(cap, base * 2^attempt))
base = 100ms, cap = 10s, max_attempts = 3
```

Retryable: 429 (respect Retry-After header), 500, 502, 503, 504, timeouts.
Non-retryable: 400, 401, 403, 404, 422.

**Retry budget** prevents amplification: allow at most 20% of base request rate as retries (minimum 1/s). Token bucket via `x/time/rate`.

### Request Coalescing

`golang.org/x/sync/singleflight` for identical in-flight non-streaming requests.

Safe to coalesce only when:
- `stream = false`
- `temperature <= 0` OR `seed` is set (deterministic output)
- `n = 1`

Use `DoChan` variant so caller context cancellation does not kill work for other waiters. Call `Forget(key)` on failure to allow retry.

### Cache Key Generation

**Include**: model, messages, temperature, top_p, max_tokens, stop, frequency_penalty, presence_penalty, seed, tools, tool_choice, response_format.
**Exclude**: stream (wire format), user (billing tag), request_id, n, logprobs.

Normalization before hashing:
1. Trim whitespace from message content (do NOT lowercase -- casing is semantic)
2. Round floats to 4 decimal places (`temperature=0.70001` == `0.7`)
3. Omit nil/zero-value optional fields
4. Marshal with stable field order

**Hash**: SHA-256 (hardware-accelerated on ARM/x86, faster than FNV-1a on modern CPUs).

Cacheability policy:
- `temperature <= 0.3` OR `seed` is set: cacheable
- `stream = true`: not cacheable (chunked, not atomic)
- `n > 1`: not cacheable

### API Key Format and Security

Format: `gnd_<base64url(32 random bytes)>` -- 256-bit entropy, URL-safe, copy-paste friendly.

```
Generate:
  1. crypto/rand.Read(32 bytes)
  2. plaintext = "gnd_" + base64url_encode(bytes)
  3. hash = hex(SHA-256(plaintext))
  4. Store: (id, hash, prefix="gnd_xxxx...", org_id, ...)
  5. Return plaintext to user ONCE, never again
```

Validation: hash the presented key, look up by hash (constant-time DB query). Belt-and-suspenders `subtle.ConstantTimeCompare` on the stored hash. Both sides are always 32 bytes so no length leak.

### Rate Limiting: Dual Token Bucket

Two independent buckets per key: **RPM** (requests/minute) and **TPM** (tokens/minute). Request must pass both.

```
Lazy refill (no background goroutine):
  tokens = min(tokens + refill_rate * elapsed, capacity)

Pre-request:
  RPM bucket: consume 1
  TPM bucket: consume estimated_prompt_tokens

Post-response:
  TPM bucket: adjust delta (estimated - actual)
```

Rate limit headers on every response (match OpenAI convention):
```
X-RateLimit-Limit-Requests, X-RateLimit-Remaining-Requests
X-RateLimit-Limit-Tokens, X-RateLimit-Remaining-Tokens
Retry-After (on 429 only)
```

### Usage Recording: Async Batched

```
Channel buffer:  1000 records (10x batch size)
Batch flush:     100 records OR 5 seconds, whichever first
Full channel:    DROP + increment usage.dropped metric (never block hot path)
Shutdown:        drain channel with 30s timeout before exit
```

Background goroutine reads from channel, accumulates batch, flushes via single `INSERT` with multiple value rows.

### Quota Enforcement: Optimistic with Ceiling

```
Pre-request:
  1. Atomic load in-memory counter
  2. If counter >= hard_limit: reject 429
  3. CAS increment counter (optimistic: assume request succeeds)

Post-response:
  4. Record actual usage via UsageRecorder (async)
  5. Periodic sync in-memory counter <-> DB (every 100ms)
```

Accepts small overage (1-5%) in exchange for zero DB round-trips on the hot path. For tight quotas (avg request > 10% of quota), use pessimistic check.

### SSE Streaming Translation

Provider format differences:

```
Provider    event: field   [DONE] sentinel   Usage location           Tool call args
OpenAI      no             data: [DONE]      separate chunk (opt-in)  streamed
Anthropic   yes (6 types)  no (message_stop) message_start + delta    streamed JSON
Gemini      no             no (EOF)          every chunk (cumulative) single complete
```

**OpenAI passthrough** (zero-copy): when upstream is OpenAI, pipe `resp.Body` directly to `ResponseWriter` with per-read `Flush()`. No parsing, no allocation.

**Anthropic/Gemini translation**: parse upstream SSE events, translate to OpenAI chunk format, emit. Use `gjson` for field extraction without full unmarshal (~1 string alloc per field vs full struct allocation).

Translation state machine for Anthropic:
```
message_start       -> emit role:"assistant" chunk, store input_tokens
content_block_delta -> emit content chunk (text_delta) or tool_calls chunk (input_json_delta)
message_delta       -> store output_tokens + stop_reason
message_stop        -> emit finish chunk, usage chunk, [DONE]
ping                -> drop
```

Stop reason mapping:
```
Anthropic end_turn     -> OpenAI stop
Anthropic max_tokens   -> OpenAI length
Anthropic tool_use     -> OpenAI tool_calls
Gemini STOP            -> OpenAI stop
Gemini MAX_TOKENS      -> OpenAI length
Gemini SAFETY          -> OpenAI content_filter
```

Allocation budget per chunk:
```
Zero-copy passthrough:        0 allocs
gjson + pre-alloc encoder:    1 string alloc
Full unmarshal + re-marshal:  N allocs (avoid)
```

SSE implementation details:
- Set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no` before first byte
- `Flush()` after every event (not every write)
- Keep-alive comment (`: keep-alive\n\n`) every 15s to prevent proxy timeouts
- Monitor `r.Context().Done()` for client disconnect, cancel upstream request
- `bufio.Scanner` with 64KB buffer for upstream SSE parsing

### HTTP Client Tuning

Per-provider `http.Client` with tuned transport:

```
MaxIdleConnsPerHost:   100    (default 2 is catastrophically low)
MaxConnsPerHost:       200
IdleConnTimeout:       90s
ForceAttemptHTTP2:     true
TLSHandshakeTimeout:  5s
```

DNS caching via `rs/dnscache` wrapped in `DialContext` -- avoids stale DNS after provider failover.

### Graceful Shutdown Sequence

```
1. Receive SIGTERM/SIGINT
2. Stop accepting new connections (http.Server.Shutdown)
3. Wait for in-flight requests to complete (context deadline: 30s)
4. Cancel upstream streaming connections
5. Drain usage recorder channel (flush remaining batch)
6. Flush OTel spans (BatchSpanProcessor.Shutdown)
7. Close DB connections
8. Exit
```

## Dependencies

| Package | Purpose | Rationale | Phase |
|---------|---------|-----------|-------|
| `github.com/go-chi/chi/v5` | HTTP router + middleware composition | Subrouter grouping, 100% http.Handler compat, zero-alloc radix tree | 1 |
| `modernc.org/sqlite` | Pure-Go SQLite (no CGO, static binary) | CGO_ENABLED=0, database/sql compat, WAL mode, actively maintained | 1 |
| `github.com/pressly/goose/v3` | DB migrations | Embedded SQL migrations, database/sql driver-agnostic | 1 |
| `github.com/maypok86/otter/v2` | In-memory cache (auth + response) | W-TinyLFU eviction, per-entry TTL, generics, ~300ns reads, no dropped writes | 1 |
| `github.com/google/uuid` | UUID v7 request IDs | RFC 9562, `EnableRandPool()` reduces syscall overhead | 1 |
| `go.yaml.in/yaml/v3` | YAML config parsing | Drop-in replacement for unmaintained `gopkg.in/yaml.v3` | 1 |
| `github.com/pkoukk/tiktoken-go` | Token counting | cl100k_base + o200k_base, matches Python tiktoken exactly | 3 |
| `golang.org/x/time/rate` | Token bucket rate limiting | Stdlib-adjacent, ~50ns Allow(), 0 allocs on hot path | 3 |
| `github.com/prometheus/client_golang` | Prometheus metrics | Native histograms (atomic ops), pre-cached label children | 4 |
| `go.opentelemetry.io/otel` + OTLP SDK | Distributed tracing | OTLP gRPC exporter (Jaeger exporter deprecated), ParentBased sampler | 4 |
| `github.com/rs/zerolog` | Zero-alloc structured logging | 30 ns/op, 0 allocs; diode writer for non-blocking async writes | 4 |
| `github.com/rs/dnscache` | DNS caching for upstream calls | Avoids DNS lookup per new connection to provider endpoints | 2 |
| `golang.org/x/sync/singleflight` | Request coalescing | Dedup identical in-flight requests, stdlib-adjacent | 6 |
| `github.com/tidwall/gjson` | Incremental JSON field extraction | ~1 alloc per field vs full unmarshal; SSE translation hot path | 2 |
| `github.com/lestrrat-go/jwx/v2` | JWT validation + JWKS cache | Full JOSE stack, background JWKS auto-refresh, stale-on-failure | 5 |

Stdlib: `net/http`, `database/sql`, `crypto/sha256`, `crypto/subtle`, `sync`, `context`, `encoding/json`, `bufio`.

### Dependency decisions

- **Cache: otter v2 over hashicorp-lru/ristretto** -- Ristretto silently drops writes under contention (lossy by design), dangerous for auth cache correctness. hashicorp-lru has single-mutex contention and goroutine leak in TTL cleanup. Otter's W-TinyLFU provides better hit rates for Zipf-distributed access patterns (typical for HTTP caches).
- **Logging: zerolog over slog** -- 5x faster (30 vs 174 ns/op), 0 allocs. On the hot path every nanosecond counts. Can expose slog.Handler interface for library compatibility if needed.
- **YAML: go.yaml.in/yaml/v3 over gopkg.in/yaml.v3** -- Original declared unmaintained April 2025. YAML org fork is drop-in replacement, identical API.
- **Token counting: pkoukk/tiktoken-go over tiktoken-go/tokenizer** -- More actively maintained (v0.1.8, Sep 2025), downloads/caches BPE vocab files rather than embedding ~4MB in binary.
- **Rate limiting: x/time/rate over custom** -- Stdlib-adjacent, battle-tested, 0 allocs on Allow(). Per-key wrapping with map+sync.RWMutex is trivial since we already have API key objects.
- **Tracing: OTLP exporter over Jaeger** -- Jaeger exporter removed from OTel SDK in 2023. Jaeger v1.35+ accepts OTLP natively. Unsampled spans cost ~10ns.
- **HTTP client: tuned net/http over fasthttp** -- fasthttp has no HTTP/2, no streaming response bodies, incompatible with otelhttp. Tuned Transport with MaxIdleConnsPerHost=100+ and ForceAttemptHTTP2=true closes the gap.
- **DNS: rs/dnscache** -- Default net.Resolver does fresh DNS lookup per new connection. dnscache wraps DialContext with configurable refresh interval.
- **JWT: lestrrat-go/jwx/v2 over coreos/go-oidc and golang-jwt** -- Full JOSE stack with JWKS auto-refresh cache (serves stale on failure). `go-oidc/v3` has a context caching bug (issue #339). `golang-jwt/v5` has no JWKS support at all -- only suitable for issuing tokens, not validating external IdP tokens.

## Implementation Phases

**Phase 1 -- Foundation (MVP):**
Project skeleton, config, SQLite + migrations, OpenAI adapter, `/v1/chat/completions` (non-streaming), API key auth (single org), health endpoint, build pipeline.

**Phase 2 -- Streaming + Multi-Provider + Native Passthrough (DONE):**
SSE streaming (channel-based, 8-buffer), Anthropic adapter (Messages API, SSE event state machine), Gemini adapter (generateContent, EOF-terminated SSE), Ollama adapter (OpenAI-compat + native API), priority failover routing (sorted targets, otter-cached), `/v1/embeddings`, `/v1/models`. Native API passthrough: `NativeProxy` interface, `ForwardRequest` shared helper, `normalizeAuth` middleware. Native routes: `/v1/messages` (Anthropic), `/v1beta/models/*` (Gemini), `/openai/deployments/*` (Azure OpenAI), `/api/*` (Ollama). Shared `dnscache.Resolver`, `gjson` for response translation + model extraction. `testutil/` package with reusable fakes. Performance: route target caching (-12 allocs), bundled request context (-2 allocs), `GOEXPERIMENT=jsonv2` (-1-8 allocs). Baseline: ~53 allocs/op ChatCompletion, ~25 allocs/op Healthz.

**Phase 3 -- Rate Limiting + Caching:**
Token-bucket rate limiter (dual RPM+TPM), exact-match cache, token counting, async usage recording, quota enforcement.

**Phase 4 -- Admin API + Observability:**
Admin CRUD endpoints (providers, keys, routes), Prometheus metrics with native histograms, OpenTelemetry tracing, usage aggregation.

**Phase 5 -- Enterprise Auth + Multi-Tenancy:**
JWT/OIDC dual-mode auth (`lestrrat-go/jwx/v2`), multi-tenant org/team hierarchy with limit inheritance, RBAC with permission bitmask, admin endpoints for orgs/teams, auth configuration endpoint.

**Phase 6 -- Advanced:**
Peak EWMA + P2C load balancing, circuit breaker with weighted failure classification, singleflight request coalescing, SSO/SAML via Dex, mTLS, OPA policy engine, semantic caching, Redis cache backend, Postgres store option.

## Verification

After each phase:
1. `go build ./...` -- compiles
2. `go fix ./...` -- adopt modern APIs (e.g., wg.Go, strings.Cut, SplitSeq)
3. `go test -race ./...` -- all tests pass
4. `go vet ./...` -- no vet issues
5. `golangci-lint run` -- no lint issues
6. `govulncheck ./...` -- no known vulnerabilities in dependencies
7. Manual smoke test: `curl -X POST http://localhost:8080/v1/chat/completions -H "Authorization: Bearer gnd-xxx" -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}'`
8. Streaming: same curl with `"stream": true`, verify SSE chunks arrive incrementally
9. Latency: `go test -bench=BenchmarkMiddlewareChain` -- verify < 5us overhead
