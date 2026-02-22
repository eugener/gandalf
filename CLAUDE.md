# Gandalf - LLM Gateway (Phase 1)

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

Hexagonal architecture. Domain types at `internal/gateway.go`, interfaces at consumer level. Phase 1 supports non-streaming `POST /v1/chat/completions` via OpenAI adapter with API key auth.

- `internal/gateway.go` -- domain types (Provider, ChatRequest/Response, APIKey, Identity, etc.) + Authenticator interface. No project imports.
- `internal/errors.go` -- sentinel errors (ErrUnauthorized, ErrNotFound, ErrRateLimited, etc.)
- `internal/auth/` -- API key auth with otter cache (30s TTL), `subtle.ConstantTimeCompare` belt-and-suspenders on hash. JWT planned for later.
- `internal/server/` -- HTTP handlers + middleware (chi). Routes wired inline in `server.go`.
- `internal/app/` -- ProxyService (route resolve + provider call), RouterService, KeyManager (create/delete keys)
- `internal/provider/` -- Registry (name->Provider map) + OpenAI adapter. Streaming/embeddings stubbed.
- `internal/storage/` -- Store interfaces in `storage.go`. SQLite impl with separate read/write pools, WAL mode, goose migrations.
- `internal/config/` -- YAML config with `${ENV}` expansion, DB bootstrap (seed providers, routes, keys)
- `cmd/gandalf/` -- Entrypoint: parse flags, wire deps, graceful shutdown via SIGTERM/SIGINT

## Directory Structure

```
gandalf/
  cmd/gandalf/
    main.go                        # Entrypoint: parse flags, call run()
    run.go                         # Wire deps, start server, graceful shutdown
  internal/
    gateway.go                     # Domain types + Authenticator interface
    errors.go                      # Sentinel errors
    auth/
      apikey.go                    # API key auth: hash -> otter cache -> DB fallback
    server/
      server.go                    # New(Deps) http.Handler, route registration (chi)
      proxy.go                     # handleChatCompletion + writeJSON + errorResponse helpers
      middleware.go                # recovery, requestID, logging, authenticate middleware
      health.go                    # handleHealthz, handleReadyz (readyz pings DB via ReadyChecker)
      server_test.go               # Handler tests with inline fakes
      server_bench_test.go         # Benchmarks: ChatCompletion, Healthz (alloc tracking)
    app/
      proxy.go                     # ProxyService: route resolve -> provider call
      router.go                    # RouterService: model alias -> provider/model resolution
      keymanager.go                # KeyManager: create/delete API keys
    provider/
      provider.go                  # Registry: thread-safe name->Provider map
      openai/
        client.go                  # OpenAI adapter (ChatCompletion, ListModels, HealthCheck)
    storage/
      storage.go                   # Store interfaces (APIKeyStore, ProviderStore, RouteStore, UsageStore, OrgStore)
      sqlite/
        db.go                      # New(dsn): open DB, run migrations, read/write pools, Ping()
        apikey.go                  # APIKeyStore impl + SQL helpers
        provider.go                # ProviderStore impl
        route.go                   # RouteStore impl
        org.go                     # OrgStore impl (orgs + teams)
        usage.go                   # UsageStore impl (batch insert)
        sqlite_test.go             # Integration tests against temp-file SQLite
        migrations/
          001_init.sql             # Schema: organizations, teams, providers, api_keys, routes, usage_records
  configs/gandalf.yaml             # Example config with env var placeholders
  Makefile
  spec.md                          # Full multi-phase design spec
```

## Dependency Flow

```
cmd/gandalf  -- wires concrete types, imports everything
  -> server  -- HTTP transport, depends on app + gateway interfaces
  -> app     -- business logic, depends on storage + provider interfaces
  -> gateway -- domain types (no project imports)
  <- provider/openai -- implements gateway.Provider
  <- storage/sqlite  -- implements storage.Store
  <- auth            -- implements gateway.Authenticator
  <- config          -- Config struct + Load() + Bootstrap()
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

## API Surface (Phase 1)

- `POST /v1/chat/completions` -- non-streaming only (auth required)
- `GET /healthz`, `GET /readyz` -- no auth

## Conventions

- Interfaces defined at consumer, not alongside implementation
- Inline fakes in `_test.go` files (no separate testutil package yet)
- Table-driven tests with `t.Parallel()`
- SQLite: separate read/write pools, WAL mode, goose embedded migrations
- Context helpers: `ContextWithIdentity`, `IdentityFromContext`, `ContextWithRequestID`, `RequestIDFromContext`
- RBAC defined as permission bitmask (`Identity.Can(p Permission) bool`), role mapping in `RolePermissions` var
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

## Testing

| Layer | Approach |
|-------|----------|
| `server/` | `httptest.NewRecorder` + inline fakes for auth/provider/store |
| `storage/sqlite/` | Temp-file SQLite, full CRUD round-trip tests |
| `config/` | Temp YAML files, env var expansion, default validation, bootstrap seed + idempotency |

## Future Phases

See `spec.md` for the full roadmap: streaming, multi-provider (Anthropic/Gemini), rate limiting, caching, admin API, observability, JWT/OIDC auth, multi-tenant RBAC, circuit breakers, and more.
