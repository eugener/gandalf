# Gandalf

LLM gateway that sits between your applications and LLM providers, adding authentication, routing, rate limiting, caching, and observability.

## Features

- **Multi-provider** -- OpenAI, Anthropic, Gemini, Ollama with unified OpenAI-compatible API
- **Native passthrough** -- forward requests directly to provider APIs without translation
- **Priority failover** -- automatic fallback across providers on errors
- **SSE streaming** -- real-time streaming with keep-alive and cancellation support
- **Rate limiting** -- per-key dual token bucket (RPM + TPM) with quota enforcement
- **Response caching** -- W-TinyLFU in-memory cache with configurable TTL
- **Admin API** -- CRUD for providers, keys, routes; usage queries with RBAC
- **Observability** -- Prometheus metrics (native histograms), OpenTelemetry tracing (OTLP gRPC)
- **Auth** -- API key authentication with per-key roles (admin/member/viewer/service_account)

## Quick Start

```bash
# Set required env vars
export OPENAI_API_KEY="sk-..."
export GANDALF_ADMIN_KEY="gnd_your_admin_key"

# Build and run
make run
```

The server starts on `:8080`. Send requests using the OpenAI-compatible API:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GANDALF_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## API

**Universal (OpenAI-format, auth required)**

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/chat/completions` | Chat completion (streaming supported) |
| POST | `/v1/embeddings` | Text embeddings |
| GET | `/v1/models` | List available models |

**Native passthrough (raw forwarding, auth required)**

| Prefix | Provider |
|--------|----------|
| `/v1/messages` | Anthropic |
| `/v1beta/models/*` | Gemini |
| `/openai/deployments/*` | Azure OpenAI |
| `/api/*` | Ollama |

**Admin API (auth + RBAC)**

| Path | Description |
|------|-------------|
| `/admin/v1/providers` | Provider CRUD |
| `/admin/v1/keys` | API key management |
| `/admin/v1/routes` | Route configuration |
| `/admin/v1/cache/purge` | Cache invalidation |
| `/admin/v1/usage` | Usage query |
| `/admin/v1/usage/summary` | Aggregated usage |

**System (no auth)**

| Path | Description |
|------|-------------|
| `/healthz` | Liveness probe |
| `/readyz` | Readiness probe |
| `/metrics` | Prometheus metrics |

## Configuration

YAML config with `${ENV_VAR}` expansion. See [`configs/gandalf.yaml`](configs/gandalf.yaml) for the full example.

```bash
./bin/gandalf -config configs/gandalf.yaml
```

Key sections: `server` (address, timeouts), `database` (SQLite DSN), `providers` (credentials, models, priority), `routes` (model alias to provider mapping), `rate_limits` (RPM/TPM defaults), `cache` (size, TTL), `keys` (bootstrap API keys with roles).

## Auth

API keys require `gnd_` prefix. Bootstrap via `GANDALF_ADMIN_KEY` env var. Per-key roles control access to admin endpoints via RBAC bitmask. Delete `gandalf.db` to re-bootstrap after changing keys.

## Development

```bash
make build      # compile binary (GOEXPERIMENT=jsonv2)
make test       # tests with race detector
make bench      # benchmarks with ns/op, rps, allocs
make lint       # go vet + golangci-lint
make check      # full pipeline: build + fix + vet + test + govulncheck + bench
make coverage   # HTML coverage report
```

## Docker

```bash
make docker
docker run -p 8080:8080 \
  -e OPENAI_API_KEY="sk-..." \
  -e GANDALF_ADMIN_KEY="gnd_..." \
  gandalf:dev -config /path/to/config.yaml
```

## Architecture

Hexagonal architecture with domain types at the center and no circular imports.

```
cmd/gandalf/           entrypoint, dependency wiring, graceful shutdown
internal/
  gateway.go           domain types + interfaces (no project imports)
  server/              HTTP handlers + middleware (chi), SSE streaming, native passthrough
  app/                 ProxyService (failover), RouterService (cached routing), KeyManager
  provider/            Registry + adapters (openai, anthropic, gemini, ollama)
  auth/                API key auth with otter cache, per-key roles
  ratelimit/           dual token bucket (RPM+TPM), Registry, QuotaTracker
  cache/               W-TinyLFU in-memory cache (otter)
  tokencount/          token estimation for TPM rate limiting
  telemetry/           Prometheus metrics, OpenTelemetry tracing
  worker/              async usage recording, quota sync, usage rollups
  storage/sqlite/      SQLite with read/write pools, WAL, goose migrations
  config/              YAML config loading + DB bootstrap
  testutil/            reusable test fakes
```

See [docs/architecture.md](docs/architecture.md) for detailed dependency flow, interfaces, streaming design, failover logic, and native passthrough.

## License

[GPL-3.0](LICENSE)
