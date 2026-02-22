# Gandalf - LLM Gateway (Phase 2)

## Build and Test

```bash
make build          # build binary
make test           # run tests with race detector
make bench          # benchmarks with ns/op, rps, allocs
make check          # full verification: build + fix + vet + test + govulncheck + bench
make lint           # go vet + golangci-lint
make run            # build and run with example config
make docker         # build Docker image
make coverage       # test coverage report
```

**Always run `make bench` after server changes and verify alloc counts stay low.**

## Architecture

Hexagonal architecture. Domain types at `internal/gateway.go`, interfaces at consumer level. Phase 2 adds SSE streaming, multi-provider support (OpenAI, Anthropic, Gemini), priority failover routing, and `/v1/embeddings` + `/v1/models` endpoints.

- `internal/gateway.go` -- domain types (Provider, ChatRequest/Response, StreamChunk, APIKey, Identity, etc.) + Authenticator interface. No project imports.
- `internal/errors.go` -- sentinel errors (ErrUnauthorized, ErrNotFound, ErrRateLimited, ErrProviderError, etc.)
- `internal/auth/` -- API key auth with otter cache (30s TTL), `subtle.ConstantTimeCompare` belt-and-suspenders on hash.
- `internal/server/` -- HTTP handlers + middleware (chi). SSE streaming support. Routes wired inline in `server.go`.
- `internal/app/` -- ProxyService (failover routing + provider call), RouterService (priority-sorted targets), KeyManager
- `internal/provider/` -- Registry (name->Provider map) + adapters for OpenAI, Anthropic, Gemini
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
    gateway.go                     # Domain types + Authenticator interface
    errors.go                      # Sentinel errors
    auth/
      apikey.go                    # API key auth: hash -> otter cache -> DB fallback
    server/
      server.go                    # New(Deps) http.Handler, route registration (chi)
      proxy.go                     # handleChatCompletion (non-stream + stream branch) + helpers
      sse.go                       # SSE write helpers: writeSSEHeaders, writeSSEData, writeSSEDone, writeSSEKeepAlive
      embeddings.go                # handleEmbeddings handler
      models.go                    # handleListModels handler (aggregates from all providers)
      middleware.go                # recovery, requestID, logging, authenticate (statusWriter supports Flush)
      health.go                    # handleHealthz, handleReadyz
      server_test.go               # Handler tests with inline fakes
      server_bench_test.go         # Benchmarks: ChatCompletion, Stream, Healthz
      sse_test.go                  # SSE write helper unit tests
      stream_test.go               # E2E streaming tests: OpenAI, Anthropic, Gemini, failover, disconnect
    app/
      proxy.go                     # ProxyService: priority failover routing for chat/stream/embeddings/models
      router.go                    # RouterService: model alias -> []ResolvedTarget sorted by priority
      keymanager.go                # KeyManager: create/delete API keys
      proxy_test.go                # Failover tests: primary ok, failover, client error, all fail
      router_test.go               # Multi-target, no route default, empty targets
    provider/
      provider.go                  # Registry: thread-safe name->Provider map
      sseutil/
        reader.go                  # Shared SSE line reader: NewScanner, ParseSSELine
        reader_test.go             # SSE parsing tests
      openai/
        client.go                  # OpenAI adapter: ChatCompletion, Stream, Embeddings, ListModels + dnscache
        client_test.go             # Stream, cancel, HTTP error, embeddings tests
      anthropic/
        client.go                  # Anthropic adapter: ChatCompletion, Stream, ListModels + dnscache
        translate.go               # OpenAI <-> Anthropic request/response translation
        stream.go                  # SSE event state machine (message_start, content_block_delta, etc.)
        client_test.go             # Translation, streaming state machine tests
      gemini/
        client.go                  # Gemini adapter: ChatCompletion, Stream, Embeddings, ListModels + dnscache
        translate.go               # OpenAI <-> Gemini request/response translation
        stream.go                  # EOF-terminated SSE reader
        client_test.go             # Translation, EOF streaming tests
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
  configs/gandalf.yaml             # Example config: OpenAI + Anthropic + Gemini
  Makefile
  spec.md
```

## Dependency Flow

```
cmd/gandalf  -- wires concrete types, DNS resolver, imports everything
  -> server  -- HTTP transport + SSE streaming, depends on app + gateway interfaces
  -> app     -- business logic, failover routing, depends on storage + provider interfaces
  -> gateway -- domain types (no project imports)
  <- provider/openai     -- implements gateway.Provider (OpenAI)
  <- provider/anthropic  -- implements gateway.Provider (Anthropic Messages API)
  <- provider/gemini     -- implements gateway.Provider (Gemini generateContent API)
  <- provider/sseutil    -- shared SSE reading utilities
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

## API Surface (Phase 2)

- `POST /v1/chat/completions` -- non-streaming and SSE streaming (auth required)
- `POST /v1/embeddings` -- embedding generation (auth required)
- `GET /v1/models` -- list models from all providers (auth required)
- `GET /healthz`, `GET /readyz` -- no auth

## Streaming Design

- Channel-based: `ChatCompletionStream` returns `<-chan StreamChunk` (buffer size 8)
- OpenAI: raw `data:` JSON passthrough, no parsing on hot path
- Anthropic: SSE event state machine translates to OpenAI-format chunks
- Gemini: EOF-terminated SSE, cumulative usage, translates to OpenAI-format chunks
- Handler: select on chunk channel, 15s keep-alive ticker, context cancellation
- `statusWriter` implements `http.Flusher` for SSE through middleware

## Priority Failover

- `RouterService.ResolveModel` returns `[]ResolvedTarget` sorted by priority (ascending)
- `ProxyService` iterates targets: on provider/network error -> try next; on client error (4xx) -> return immediately
- Applied to ChatCompletion, ChatCompletionStream, and Embeddings

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

## Dependencies (go.mod)

| Package | Purpose |
|---------|---------|
| `go-chi/chi/v5` | HTTP router |
| `google/uuid` | UUID v7 for request IDs and entity IDs |
| `maypok86/otter/v2` | W-TinyLFU in-memory cache for API key auth |
| `pressly/goose/v3` | Embedded SQL migrations |
| `go.yaml.in/yaml/v3` | YAML config parsing |
| `modernc.org/sqlite` | Pure-Go SQLite (no CGO) |
| `rs/dnscache` | Shared DNS cache for all provider HTTP transports |
| `tidwall/gjson` | Zero-alloc JSON field extraction for response translation |

## Testing

| Layer | Approach |
|-------|----------|
| `server/` | `httptest.NewRecorder` + inline fakes; E2E streaming with real provider adapters |
| `app/` | Table-driven proxy failover + router priority tests with `testutil.FakeProvider` |
| `provider/openai/` | `httptest.NewServer` for streaming, cancellation, HTTP errors, embeddings |
| `provider/anthropic/` | Request/response translation, SSE event state machine with canned events |
| `provider/gemini/` | Request/response translation, EOF-terminated streaming |
| `provider/sseutil/` | SSE line parsing unit tests |
| `storage/sqlite/` | Temp-file SQLite, full CRUD round-trip tests |
| `config/` | YAML parsing, env var expansion, bootstrap seed + idempotency |

## Benchmark Baseline

```
ChatCompletion:       ~68 allocs/op  ~4.4us
ChatCompletionStream: ~74 allocs/op  ~4.4us
Healthz:              ~25 allocs/op  ~1.9us
```

Remaining alloc bottleneck is `encoding/json` (~20 allocs) -- needs json/v2 or third-party lib.

## Future Phases

See `spec.md` for the full roadmap: rate limiting, caching, admin API, observability, JWT/OIDC auth, multi-tenant RBAC, circuit breakers, and more.
