# Architecture Reference

## Directory Structure

```
gandalf/
  cmd/gandalf/
    main.go                        # Entrypoint: parse flags, call run()
    run.go                         # Wire deps, DNS cache, startup config logging, start server, graceful shutdown
  internal/
    gateway.go                     # Domain types + Provider/NativeProxy/Authenticator interfaces + bundled requestMeta context
    errors.go                      # Sentinel errors
    auth/
      apikey.go                    # API key auth: hash -> otter cache -> DB fallback
    server/
      server.go                    # New(Deps) http.Handler, route registration (chi), dep interfaces
      admin.go                     # Admin CRUD handlers: providers, keys, routes, cache purge, usage query
      proxy.go                     # handleChatCompletion (non-stream + stream), TPM consume/adjust, usage recording, cache
      cache.go                     # cacheKey (SHA-256), isCacheable, Cache interface
      native.go                    # Native API passthrough: handleNativeProxy, normalizeAuth, route mounting
      sse.go                       # SSE write helpers: writeSSEHeaders, writeSSEData, writeSSEDone, writeSSEKeepAlive
      embeddings.go                # handleEmbeddings handler with TPM + usage recording
      models.go                    # handleListModels handler (aggregates from all providers)
      metrics.go                   # metricsMiddleware (duration, status, active count), routePattern helper
      middleware.go                # recovery, requestID, logging, authenticate, rateLimit, requirePerm, tracing
      health.go                    # handleHealthz, handleReadyz
      server_test.go               # Handler tests with inline fakes
      admin_test.go                # Admin CRUD + RBAC enforcement tests
      metrics_test.go              # Prometheus metrics integration tests
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
    ratelimit/
      ratelimit.go                 # Dual token bucket (RPM+TPM), Limiter, Registry
      quota.go                     # QuotaTracker: in-memory budget tracking
      *_test.go
    cache/
      cache.go                     # Cache interface (Get/Set/Delete/Purge)
      memory.go                    # In-memory W-TinyLFU cache (otter) with per-entry TTL
      memory_test.go
    tokencount/
      tokencount.go                # Token estimation (~4 chars/token heuristic)
      tokencount_test.go
    worker/
      worker.go                    # Worker interface: Run(ctx) error
      runner.go                    # errgroup-based runner, cancel-on-first-error
      usage_recorder.go            # Buffered channel -> batch DB flush (100 records or 5s)
      usage_rollup.go              # Periodic aggregation of raw usage into hourly rollups
      quota_sync.go                # Periodic quota counter reload from DB (60s)
      *_test.go
    storage/
      storage.go                   # Store interfaces (APIKeyStore, UsageStore, etc.)
      sqlite/
        db.go, apikey.go, provider.go, route.go, org.go, usage.go
        sqlite_test.go
        migrations/001_init.sql, 002_key_role.sql, 003_usage_rollups.sql
    telemetry/
      metrics.go                     # Prometheus Metrics struct + NewMetrics(registerer)
      tracing.go                     # SetupTracing (OTLP gRPC) + Tracer() helper
    testutil/
      fake_provider.go             # FakeProvider with configurable callbacks + FakeStreamChan
      fake_store.go                # In-memory FakeStore implementing storage.Store
      fake_auth.go                 # FakeAuth (always succeeds) + RejectAuth
    config/
      config.go, bootstrap.go, *_test.go
  configs/gandalf.yaml             # Example config: OpenAI + Anthropic + Gemini + Ollama
  Makefile
  docs/
    architecture.md
    performance.md
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
    APIKeyStore   // CreateKey, GetKey, GetKeyByHash, ListKeys, CountKeys, UpdateKey, DeleteKey, TouchKeyUsed
    ProviderStore // CreateProvider, GetProvider, ListProviders, CountProviders, UpdateProvider, DeleteProvider
    RouteStore    // CreateRoute, GetRoute, GetRouteByAlias, ListRoutes, CountRoutes, UpdateRoute, DeleteRoute
    UsageStore    // InsertUsage, SumUsageCost, QueryUsage, CountUsage, UpsertRollup, QueryRollups
    OrgStore      // CRUD for orgs + teams
    Close() error
}
```

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

## Startup Logging

On startup, `run.go` logs configuration for debuggability:

- `starting gandalf` -- version (from git tag) and listen address
- `database opened` -- DSN path
- `api key configured` -- key name + `valid_prefix` (whether key has `gnd_` prefix); never logs key material
- `provider registered` -- provider name + `native_proxy` support; or `provider skipped (disabled)`
- `route configured` -- model alias + target list (e.g. `openai/gpt-4o`)
- `server timeouts` -- read/write/shutdown durations
- `universal API enabled` -- list of universal endpoints
- `gandalf ready` -- final ready signal
