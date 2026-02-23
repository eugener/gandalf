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
```

All make targets set `GOEXPERIMENT=jsonv2` for lower alloc counts in JSON-heavy hot paths.

**After any code change: `make build && make test`. After server changes: also `make bench` and verify alloc counts stay low.**

## Architecture

Hexagonal architecture. Domain types at `internal/gateway.go`, interfaces at consumer level. Multi-provider support (OpenAI, Anthropic, Gemini, Ollama), priority failover routing, SSE streaming, native API passthrough.

Key packages:
- `internal/gateway.go` -- domain types + interfaces (no project imports)
- `internal/server/` -- HTTP handlers + middleware (chi), SSE streaming, native passthrough
- `internal/app/` -- ProxyService (failover), RouterService (cached routing), KeyManager
- `internal/provider/` -- Registry + adapters (openai, anthropic, gemini, ollama)
- `internal/storage/sqlite/` -- SQLite with read/write pools, WAL, goose migrations
- `internal/config/` -- YAML config with `${ENV}` expansion, DB bootstrap
- `internal/auth/` -- API key auth with otter cache
- `internal/testutil/` -- reusable test fakes
- `cmd/gandalf/` -- entrypoint, wiring, startup logging, graceful shutdown

See [docs/architecture.md](docs/architecture.md) for full directory listing, dependency flow, interfaces, streaming design, failover, and native passthrough details.

## API Surface

**Universal (OpenAI-format, auth required):** `POST /v1/chat/completions`, `POST /v1/embeddings`, `GET /v1/models`

**Native passthrough (raw forwarding):** Anthropic `/v1/messages`, Gemini `/v1beta/models/*`, Azure `/openai/deployments/*`, Ollama `/api/*`

**System (no auth):** `GET /healthz`, `GET /readyz`

## Auth

API keys require `gnd_` prefix. Set via `GANDALF_ADMIN_KEY` env var. Delete `gandalf.db` to re-bootstrap after changing keys.

## Dependencies

| Package | Purpose |
|---------|---------|
| `go-chi/chi/v5` | HTTP router |
| `google/uuid` | UUID v7 |
| `maypok86/otter/v2` | W-TinyLFU cache (auth + routing) |
| `pressly/goose/v3` | SQL migrations |
| `go.yaml.in/yaml/v3` | YAML config |
| `modernc.org/sqlite` | Pure-Go SQLite |
| `rs/dnscache` | Shared DNS cache |
| `tidwall/gjson` | Zero-alloc JSON field extraction |

## Conventions

- Interfaces defined at consumer, not alongside implementation
- Table-driven tests with `t.Parallel()`
- Inline fakes in `server_test.go`; `testutil/` for reusable fakes
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
