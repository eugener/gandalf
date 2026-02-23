# Gandalf - LLM Gateway (Phase 2)

## Build and Test

```bash
make build          # build binary (GOEXPERIMENT=jsonv2)
make test           # run tests with race detector
make bench          # benchmarks with ns/op, rps, allocs
make check          # full verification: build + fix + vet + test + govulncheck + bench
make lint           # go vet + golangci-lint
make run            # build and run with example config
make docker         # build Docker image
make coverage       # test coverage report
```

All make targets set `GOEXPERIMENT=jsonv2` for lower alloc counts in JSON-heavy hot paths.

**Always run `make bench` after server changes and verify alloc counts stay low.**

## Architecture

Hexagonal architecture. Domain types at `internal/gateway.go`, interfaces at consumer level. Multi-provider support (OpenAI, Anthropic, Gemini, Ollama), priority failover routing, SSE streaming, native API passthrough, and `/v1/embeddings` + `/v1/models` endpoints.

- `internal/gateway.go` -- domain types (Provider, NativeProxy, ChatRequest/Response, StreamChunk, APIKey, Identity, etc.) + Authenticator interface. Bundled `requestMeta` context (single alloc for requestID + identity). No project imports.
- `internal/errors.go` -- sentinel errors (ErrUnauthorized, ErrNotFound, ErrRateLimited, ErrProviderError, etc.)
- `internal/auth/` -- API key auth with otter cache (30s TTL), `subtle.ConstantTimeCompare` belt-and-suspenders on hash.
- `internal/server/` -- HTTP handlers + middleware (chi). SSE streaming support. Native passthrough routes in `native.go`. Routes wired inline in `server.go`.
- `internal/app/` -- ProxyService (failover routing + provider call), RouterService (priority-sorted targets, otter-cached), KeyManager
- `internal/provider/` -- Registry (name->Provider map) + ForwardRequest shared helper + adapters for OpenAI, Anthropic, Gemini, Ollama
- `internal/provider/sseutil/` -- shared SSE line reader for all provider adapters
- `internal/storage/` -- Store interfaces in `storage.go`. SQLite impl with separate read/write pools, WAL mode, goose migrations.
- `internal/config/` -- YAML config with `${ENV}` expansion, DB bootstrap (seed providers, routes, keys)
- `internal/testutil/` -- reusable test fakes (FakeProvider, FakeStore, FakeAuth, RejectAuth)
- `cmd/gandalf/` -- Entrypoint: parse flags, wire deps, shared DNS cache, graceful shutdown

## Directory Structure

```
gandalf/
  cmd/gandalf/
    main.go                        # Entrypoint: parse flags, call run()
    run.go                         # Wire deps, DNS cache, start server, graceful shutdown
  internal/
    gateway.go                     # Domain types + Provider/NativeProxy/Authenticator interfaces + bundled requestMeta context
    errors.go                      # Sentinel errors
    auth/
      apikey.go                    # API key auth: hash -> otter cache -> DB fallback
    server/
      server.go                    # New(Deps) http.Handler, route registration (chi)
      proxy.go                     # handleChatCompletion (non-stream + stream branch) + helpers
      native.go                    # Native API passthrough: handleNativeProxy, normalizeAuth, route mounting
      sse.go                       # SSE write helpers: writeSSEHeaders, writeSSEData, writeSSEDone, writeSSEKeepAlive
      embeddings.go                # handleEmbeddings handler
      models.go                    # handleListModels handler (aggregates from all providers)
      middleware.go                # recovery, requestID, logging, authenticate (statusWriter supports Flush)
      health.go                    # handleHealthz, handleReadyz
      server_test.go               # Handler tests with inline fakes
      server_bench_test.go         # Benchmarks: ChatCompletion, Stream, Healthz
      native_test.go               # Native passthrough E2E tests: Anthropic, Gemini, Azure, Ollama
      sse_test.go                  # SSE write helper unit tests
      stream_test.go               # E2E streaming tests: OpenAI, Anthropic, Gemini, failover, disconnect
    app/
      proxy.go                     # ProxyService: priority failover routing for chat/stream/embeddings/models
      router.go                    # RouterService: model alias -> []ResolvedTarget (otter-cached, 10s TTL)
      keymanager.go                # KeyManager: create/delete API keys
      proxy_test.go                # Failover tests: primary ok, failover, client error, all fail
      router_test.go               # Multi-target, no route default, empty targets
    provider/
      provider.go                  # Registry: thread-safe name->Provider map
      proxy.go                     # ForwardRequest: shared native HTTP passthrough helper
      proxy_test.go                # ForwardRequest tests: headers, SSE flush, upstream errors
      sseutil/
        reader.go                  # Shared SSE line reader: NewScanner, ParseSSELine
        reader_test.go             # SSE parsing tests
      openai/
        client.go                  # OpenAI adapter: ChatCompletion, Stream, Embeddings, ListModels, ProxyRequest + dnscache
        client_test.go             # Stream, cancel, HTTP error, embeddings tests
      anthropic/
        client.go                  # Anthropic adapter: ChatCompletion, Stream, ListModels, ProxyRequest + dnscache
        translate.go               # OpenAI <-> Anthropic request/response translation
        stream.go                  # SSE event state machine (message_start, content_block_delta, etc.)
        client_test.go             # Translation, streaming state machine tests
      gemini/
        client.go                  # Gemini adapter: ChatCompletion, Stream, Embeddings, ListModels, ProxyRequest + dnscache
        translate.go               # OpenAI <-> Gemini request/response translation
        stream.go                  # EOF-terminated SSE reader
        client_test.go             # Translation, EOF streaming tests
      ollama/
        client.go                  # Ollama adapter: ChatCompletion, Stream, Embeddings, ListModels, ProxyRequest + dnscache
        client_test.go             # Completion, stream, list models, proxy request tests
    storage/
      storage.go                   # Store interfaces
      sqlite/
        db.go, apikey.go, provider.go, route.go, org.go, usage.go
        sqlite_test.go
        migrations/001_init.sql
    testutil/
      fake_provider.go             # FakeProvider with configurable callbacks + FakeStreamChan
      fake_store.go                # In-memory FakeStore implementing storage.Store
      fake_auth.go                 # FakeAuth (always succeeds) + RejectAuth
    config/
      config.go, bootstrap.go, *_test.go
  configs/gandalf.yaml             # Example config: OpenAI + Anthropic + Gemini + Ollama
  Makefile
  spec.md
```

## Dependency Flow

```
cmd/gandalf  -- wires concrete types, DNS resolver, imports everything
  -> server  -- HTTP transport + SSE streaming, depends on app + gateway interfaces
  -> app     -- business logic, failover routing, depends on storage + provider interfaces
  -> gateway -- domain types (no project imports)
  <- provider/openai     -- implements gateway.Provider + NativeProxy (OpenAI)
  <- provider/anthropic  -- implements gateway.Provider + NativeProxy (Anthropic Messages API)
  <- provider/gemini     -- implements gateway.Provider + NativeProxy (Gemini generateContent API)
  <- provider/ollama     -- implements gateway.Provider + NativeProxy (Ollama)
  <- provider/sseutil    -- shared SSE reading utilities
  <- provider/proxy.go   -- shared ForwardRequest helper for NativeProxy implementations
  <- storage/sqlite      -- implements storage.Store
  <- auth                -- implements gateway.Authenticator
  <- config              -- Config struct + Load() + Bootstrap()
  <- testutil            -- reusable test fakes
```

## Key Interfaces

```go
// internal/gateway.go
type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    ChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
    Embeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
    ListModels(ctx context.Context) ([]string, error)
    HealthCheck(ctx context.Context) error
}

// Optional interface for native API passthrough (checked via type assertion)
type NativeProxy interface {
    ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, path string) error
}

type Authenticator interface {
    Authenticate(ctx context.Context, r *http.Request) (*Identity, error)
}
```

```go
// internal/storage/storage.go
type Store interface {
    APIKeyStore   // CreateKey, GetKeyByHash, ListKeys, UpdateKey, DeleteKey, TouchKeyUsed
    ProviderStore // CreateProvider, GetProvider, ListProviders, UpdateProvider, DeleteProvider
    RouteStore    // CreateRoute, GetRouteByAlias, ListRoutes, UpdateRoute, DeleteRoute
    UsageStore    // InsertUsage(ctx, []UsageRecord)
    OrgStore      // CRUD for orgs + teams
    Close() error
}
```

## API Surface

### Universal API (OpenAI-format, translated)

- `POST /v1/chat/completions` -- non-streaming and SSE streaming (auth required)
- `POST /v1/embeddings` -- embedding generation (auth required)
- `GET /v1/models` -- list models from all providers (auth required)

### Native API Passthrough (raw forwarding, zero translation)

- `POST /v1/messages` -- Anthropic Messages API (auth: `x-api-key` or `Authorization`)
- `POST /v1beta/models/{model}:generateContent` -- Gemini (auth: `x-goog-api-key` or `Authorization`)
- `POST /v1beta/models/{model}:streamGenerateContent` -- Gemini streaming
- `POST /v1beta/models/{model}:embedContent` -- Gemini embeddings
- `GET /v1beta/models` -- Gemini list models
- `POST /openai/deployments/{dep}/chat/completions` -- Azure OpenAI (auth: `api-key` or `Authorization`)
- `POST /openai/deployments/{dep}/embeddings` -- Azure OpenAI embeddings
- `POST /api/chat` -- Ollama native chat
- `POST /api/embed` -- Ollama native embeddings
- `GET /api/tags` -- Ollama list models

### System

- `GET /healthz`, `GET /readyz` -- no auth

## Streaming Design

- Channel-based: `ChatCompletionStream` returns `<-chan StreamChunk` (buffer size 8)
- OpenAI: raw `data:` JSON passthrough, no parsing on hot path
- Anthropic: SSE event state machine translates to OpenAI-format chunks
- Gemini: EOF-terminated SSE, cumulative usage, translates to OpenAI-format chunks
- Handler: select on chunk channel, 15s keep-alive ticker, context cancellation
- `statusWriter` implements `http.Flusher` for SSE through middleware

## Priority Failover

- `RouterService.ResolveModel` returns `[]ResolvedTarget` sorted by priority (ascending), cached via otter (10s TTL)
- `ProxyService` iterates targets: on provider/network error -> try next; on client error (4xx) -> return immediately
- Applied to ChatCompletion, ChatCompletionStream, and Embeddings

## Native API Passthrough

Two modes coexist:
1. **Universal API** (existing): `POST /v1/chat/completions` -- OpenAI format, works with ANY model via translation
2. **Native passthrough** (new): `/v1/messages`, `/v1beta/models/*`, etc. -- raw forwarding to the matching provider

- `NativeProxy` interface (optional, checked via type assertion) -- all four providers implement it
- `provider.ForwardRequest` shared helper handles URL construction, header forwarding, auth injection, and flush-on-read streaming
- `normalizeAuth` middleware maps provider-specific auth headers (`x-api-key`, `x-goog-api-key`, `api-key`) to `Authorization: Bearer` before the existing `authenticate` middleware
- Model extracted from request body (`gjson`) or URL params, routed through existing `RouterService`

## Performance Optimizations

- **Route target caching**: otter W-TinyLFU cache (10s TTL) in RouterService eliminates per-request json.Unmarshal of route targets. Saved ~12 allocs/op.
- **Bundled request context**: single `requestMeta` struct holds both requestID and identity. authenticate middleware mutates the existing pointer instead of creating a new context.WithValue + Request.WithContext. Saved 2 allocs/op.
- **GOEXPERIMENT=jsonv2**: encoding/json/v2 reduces allocs in JSON decode/encode. Saves ~1-8 allocs/op depending on path. Set globally in Makefile.
- **Pre-allocated byte slices**: SSE framing (`data: `, `\n\n`, `[DONE]`), Content-Type headers avoid per-request allocations.
- **sync.Pool for statusWriter**: middleware reuses status-capturing wrappers.
- **Direct header map access**: skips MIME canonicalization (1 alloc/req saved per header).

## Conventions

- Interfaces defined at consumer, not alongside implementation
- Inline fakes in `server_test.go` for existing tests; `testutil/` package for Phase 2+ tests
- Table-driven tests with `t.Parallel()`
- Provider apiError types implement `HTTPStatus() int` for failover decisions
- Shared `dnscache.Resolver` passed to all provider constructors, refreshed every 5 min
- Context helpers: `ContextWithIdentity`, `IdentityFromContext`, `ContextWithRequestID`, `RequestIDFromContext`
- RBAC defined as permission bitmask, role mapping in `RolePermissions`
- Config supports `${ENV_VAR}` expansion in YAML
- Bootstrap seeds providers/routes/keys from config on first run (idempotent)
- `log/slog` for logging (stdlib)
- Before committing, sync `CLAUDE.md` and `spec.md` to reflect current project state
- After each coding session, review test coverage (`make coverage`) and add tests for any new or newly-uncovered code -- aim for the best reasonable coverage without testing trivial wiring

## Dependencies (go.mod)

| Package | Purpose |
|---------|---------|
| `go-chi/chi/v5` | HTTP router |
| `google/uuid` | UUID v7 for request IDs and entity IDs |
| `maypok86/otter/v2` | W-TinyLFU cache for API key auth + route target caching |
| `pressly/goose/v3` | Embedded SQL migrations |
| `go.yaml.in/yaml/v3` | YAML config parsing |
| `modernc.org/sqlite` | Pure-Go SQLite (no CGO) |
| `rs/dnscache` | Shared DNS cache for all provider HTTP transports |
| `tidwall/gjson` | Zero-alloc JSON field extraction for response translation |

## Testing

| Layer | Approach |
|-------|----------|
| `server/` | `httptest.NewRecorder` + inline fakes; E2E streaming with real provider adapters; native passthrough E2E |
| `app/` | Table-driven proxy failover + router priority tests with `testutil.FakeProvider` |
| `provider/` | ForwardRequest helper: header forwarding, SSE flush, upstream errors |
| `provider/openai/` | `httptest.NewServer` for streaming, cancellation, HTTP errors, embeddings |
| `provider/anthropic/` | Request/response translation, SSE event state machine with canned events |
| `provider/gemini/` | Request/response translation, EOF-terminated streaming |
| `provider/ollama/` | `httptest.NewServer` for completions, streaming, list models, proxy request |
| `provider/sseutil/` | SSE line parsing unit tests |
| `storage/sqlite/` | Temp-file SQLite, full CRUD round-trip tests |
| `config/` | YAML parsing, env var expansion, bootstrap seed + idempotency |

## Benchmark Baseline

With `GOEXPERIMENT=jsonv2` (set in Makefile):

```
ChatCompletion:       ~53 allocs/op  ~4.9us
ChatCompletionStream: ~52 allocs/op  ~4.7us
Healthz:              ~25 allocs/op  ~2.2us
```

Without jsonv2: ChatCompletion ~54, Stream ~60.

Remaining ~18 allocs from `encoding/json` (request decode + response encode). Only reducible via `easyjson` codegen or waiting for json/v2 to graduate from experiment.

CPU profile shows 96% of time in runtime (GC + scheduling), only 4% in application code. Alloc reduction IS the throughput optimization.

## Future Phases

See `spec.md` for the full roadmap: rate limiting, caching, admin API, observability, JWT/OIDC auth, multi-tenant RBAC, circuit breakers, and more.
