# Gandalf - LLM Gateway

## Build and Test

```bash
make build          # build binary (GOEXPERIMENT=jsonv2)
make test           # run tests with race detector
make bench          # benchmarks with ns/op, rps, allocs
make check          # full verification: build + fix + vet + test + govulncheck + bench
make lint           # go vet + golangci-lint
make run            # build and run with example config
make coverage       # test coverage report
make clean          # remove bin/ and coverage.out
make docker         # build Docker image with VERSION tag
```

All make targets set `GOEXPERIMENT=jsonv2` for lower alloc counts in JSON-heavy hot paths.

**After any code change: `make build && make test`. After server changes: also `make bench` and verify alloc counts stay low.**

## Architecture

Hexagonal architecture. Domain types at `internal/gateway.go`, interfaces at consumer level. Multi-provider support (OpenAI, Anthropic, Gemini, Ollama) with Name/Type split (instance ID vs wire format), priority failover routing, SSE streaming, native API passthrough. Cloud hosting: Azure OpenAI (API key auth), Vertex AI (GCP OAuth ADC) for Gemini/Anthropic, and AWS Bedrock (SigV4) for Anthropic with URL rewriting. Auth extracted into `http.RoundTripper` decorators -- adapters are unaware of cloud auth. Per-key rate limiting, response caching, async usage recording, quota enforcement. Admin CRUD API with RBAC, usage aggregation, Prometheus metrics, OpenTelemetry tracing.

Key packages:
- `internal/gateway.go` -- domain types + interfaces (no project imports)
- `internal/server/` -- HTTP handlers + middleware (chi), SSE streaming, native passthrough, admin CRUD, metrics/tracing middleware
- `internal/app/` -- ProxyService (failover with tracing spans), RouterService (cached routing), KeyManager
- `internal/provider/` -- Registry + adapters (openai, anthropic, gemini, ollama)
- `internal/cloudauth/` -- `http.RoundTripper` decorators: `APIKeyTransport`, `GCPOAuthTransport` (ADC), `AWSSigV4Transport` (SigV4)
- `internal/ratelimit/` -- dual token bucket (RPM+TPM), Registry, QuotaTracker
- `internal/cache/` -- Cache interface, otter W-TinyLFU memory implementation
- `internal/tokencount/` -- token estimation for TPM rate limiting
- `internal/telemetry/` -- Prometheus metrics (Metrics struct), OpenTelemetry tracing (OTLP gRPC)
- `internal/worker/` -- Worker interface, Runner (errgroup), UsageRecorder, QuotaSyncWorker, UsageRollupWorker
- `internal/storage/sqlite/` -- SQLite with read/write pools, WAL, goose migrations
- `internal/config/` -- YAML config with `${ENV}` expansion, DB bootstrap, telemetry config
- `internal/auth/` -- API key auth with otter cache, per-key roles
- `internal/testutil/` -- reusable test fakes
- `cmd/gandalf/` -- entrypoint, wiring, transport chain assembly, startup logging, graceful shutdown

See [docs/architecture.md](docs/architecture.md) for full directory listing, dependency flow, interfaces, streaming design, failover, and native passthrough details.

## API Surface

**Universal (OpenAI-format, auth required):** `POST /v1/chat/completions`, `POST /v1/embeddings`, `GET /v1/models`

**Native passthrough (raw forwarding):** Anthropic `/v1/messages`, Gemini `/v1beta/models/*`, Azure `/openai/deployments/*`, Ollama `/api/*`

**Admin API (auth + RBAC):** `/admin/v1/providers`, `/admin/v1/keys`, `/admin/v1/routes`, `/admin/v1/cache/purge`, `/admin/v1/usage`, `/admin/v1/usage/summary`

**System (no auth):** `GET /healthz`, `GET /readyz`, `GET /metrics`

## Auth

API keys require `gnd_` prefix. Set via `GANDALF_ADMIN_KEY` env var. Delete `gandalf.db` to re-bootstrap after changing keys. Per-key roles (admin/member/viewer/service_account) control access to admin endpoints via RBAC bitmask.

## Dependencies

| Package | Purpose |
|---------|---------|
| `go-chi/chi/v5` | HTTP router |
| `google/uuid` | UUID v7 |
| `maypok86/otter/v2` | W-TinyLFU cache (auth, routing, response cache) |
| `pressly/goose/v3` | SQL migrations |
| `go.yaml.in/yaml/v3` | YAML config |
| `modernc.org/sqlite` | Pure-Go SQLite |
| `rs/dnscache` | Shared DNS cache |
| `tidwall/gjson` | Zero-alloc JSON field extraction |
| `golang.org/x/sync` | errgroup for worker management |
| `prometheus/client_golang` | Prometheus metrics (native histograms) |
| `go.opentelemetry.io/otel` | Distributed tracing (OTLP gRPC) |
| `golang.org/x/oauth2` | GCP OAuth2 ADC for Vertex AI cloud auth |
| `aws/aws-sdk-go-v2` | AWS SigV4 signing, credential chain, event stream for Bedrock |

## Performance Rules

Every change to `internal/server/` or `internal/app/` hot paths must preserve alloc counts. Run `make bench` before and after; if allocs/op increase, fix before committing. Current baseline (with `GOEXPERIMENT=jsonv2`):

```
ChatCompletion:       41 allocs/op
ChatCompletionStream: 44 allocs/op
Healthz:              25 allocs/op
```

Known pitfalls on hot paths:
- **No Go generics**: shape dictionary + closure causes +1 alloc/op. Use concrete `any` params or inline loops instead
- **No closures in failover loops**: each closure literal is a heap alloc. Use method calls or helper functions with explicit params
- **No `slog.Info`/`slog.Error`**: boxes every arg into `any`. Use `slog.LogAttrs` with typed attrs (`slog.String`, `slog.Int`)
- **No `Header.Set`/`Header.Get`**: creates `[]string{v}` each call. Use direct map access (`w.Header()["Key"] = preAllocSlice`)
- **Pool buffers**: use `bodyPool` (sync.Pool) for request body reads, not `json.NewDecoder` (cannot be pooled)
- **Lazy tickers**: defer `time.NewTicker` until first use (saves ~3 allocs on short streams)

See [docs/performance.md](docs/performance.md) for full optimization history.

## Conventions

- Interfaces defined at consumer, not alongside implementation
- Table-driven tests with `t.Parallel()`
- Inline fakes in `server_test.go`; `testutil/` for reusable fakes
- Provider constructors: `New(name, baseURL string, client *http.Client)`. Auth via transport chain, not in adapters
- Provider `Name()` = instance ID (registry key, DB PK), `Type()` = wire format (e.g. "openai"). Config `type` defaults to `name` for backward compat
- Cloud hosting: `NewWithHosting(name, baseURL, client, hosting, region, project)` for Vertex/Bedrock URL rewriting (Anthropic, Gemini)
- Config `ProviderEntry`: `hosting` ("", "azure", "vertex", "bedrock"), `region`, `project`, `auth` sub-struct. `ResolvedAuthType()` infers from hosting
- Bedrock streaming uses AWS binary event stream protocol (not SSE); native proxy returns 501 for Bedrock
- Provider `apiError` types implement `HTTPStatus() int` for failover decisions
- Context helpers: `ContextWithIdentity`, `IdentityFromContext`, `ContextWithRequestID`, `RequestIDFromContext`
- Config supports `${ENV_VAR}` expansion; bootstrap seeds on first run (idempotent)
- `log/slog` for logging
- Before committing, sync `CLAUDE.md` and `docs/spec.md` to reflect current project state; keep CLAUDE.md terse and extract details into `docs/*.md`
- After each coding session, look for codebase improvements using best practices and DRY principle
- After each coding session, review test coverage and add tests for new code

## Reference

- [docs/architecture.md](docs/architecture.md) -- directory structure, dependency flow, interfaces, streaming, failover, native passthrough, startup logging
- [docs/performance.md](docs/performance.md) -- optimizations, benchmark baseline, testing approach per layer
- [docs/spec.md](docs/spec.md) -- full roadmap: rate limiting, caching, admin API, observability, JWT/OIDC, RBAC, circuit breakers
