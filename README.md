# Gandalf

[![CI](https://github.com/eugener/gandalf/actions/workflows/ci.yml/badge.svg)](https://github.com/eugener/gandalf/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/eugener/gandalf/badges/.badges/coverage.json)](https://github.com/eugener/gandalf/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/eugener/gandalf)](https://goreportcard.com/report/github.com/eugener/gandalf)
[![Go Version](https://img.shields.io/github/go-mod/go-version/eugener/gandalf)](https://go.dev/)
[![License](https://img.shields.io/github/license/eugener/gandalf)](LICENSE)

LLM gateway that sits between your applications and LLM providers, adding authentication, routing, rate limiting, caching, and observability.

## Features

### Core Gateway
- [x] Multi-provider support (OpenAI, Anthropic, Gemini, Ollama)
- [x] Unified OpenAI-compatible API (`/v1/chat/completions`, `/v1/embeddings`, `/v1/models`)
- [x] Multi-instance providers (e.g. `openai-us`, `openai-eu`) with independent credentials
- [x] Priority failover routing across providers on errors
- [x] SSE streaming with keep-alive and client disconnect detection
- [x] Native API passthrough (Anthropic, Gemini, Azure, Ollama) without translation
- [x] YAML config with `${ENV_VAR}` expansion
- [x] Graceful shutdown with in-flight request draining

### Cloud Hosting
- [x] Azure OpenAI (API key auth)
- [x] GCP Vertex AI for Gemini and Anthropic (OAuth ADC)
- [x] AWS Bedrock for Anthropic (SigV4 signing, binary event stream)
- [ ] Azure Entra ID (OAuth2 token auth)

### Auth and Access Control
- [x] API key authentication (`gnd_` prefix, SHA-256 hashed)
- [x] Per-key roles (admin / member / viewer / service_account)
- [x] RBAC with permission bitmask (no DB lookup on hot path)
- [x] Per-key model allowlists
- [ ] JWT/OIDC dual-mode auth (JWKS auto-refresh, claim mapping)
- [ ] Multi-tenant org/team hierarchy with limit inheritance
- [ ] SSO/SAML via Dex companion service

### Rate Limiting and Quotas
- [x] Dual token bucket (RPM + TPM) per key
- [x] Config-level default RPM/TPM fallback
- [x] Rate limit headers (X-Ratelimit-Limit/Remaining, Retry-After)
- [x] Quota enforcement with in-memory spend tracking
- [x] Periodic quota sync from DB

### Caching
- [x] W-TinyLFU in-memory response cache (otter)
- [x] Deterministic cache keys (SHA-256, normalized messages)
- [x] Route-configurable cache TTL per model
- [ ] Semantic caching (embedding similarity)
- [ ] Redis cache backend

### Observability
- [x] Prometheus metrics (native histograms, request duration, tokens processed, cache hits/misses, rate limit rejects)
- [x] OpenTelemetry distributed tracing (OTLP gRPC)
- [x] Structured logging (log/slog)
- [x] Per-request tracing spans with provider attribution

### Admin API
- [x] Provider CRUD (`/admin/v1/providers`)
- [x] API key management (`/admin/v1/keys`)
- [x] Route configuration (`/admin/v1/routes`)
- [x] Cache purge (`/admin/v1/cache/purge`)
- [x] Usage query and summary (`/admin/v1/usage`, `/admin/v1/usage/summary`)
- [ ] Org/team CRUD (`/admin/v1/organizations`, `/admin/v1/teams`)
- [ ] Auth configuration endpoint (`/admin/v1/auth/configure`)

### Usage and Billing
- [x] Async batched usage recording (buffered channel, no hot-path blocking)
- [x] Hourly usage rollups (background worker)
- [x] Per-request cost estimation
- [x] Usage filtering by org, key, model, time range

### Resilience
- [x] Circuit breaker with weighted failure classification (sliding window, per-provider)
- [ ] Exponential backoff with jitter retry strategy
- [ ] Retry budget (cap retries at 20% of base rate)
- [ ] Peak EWMA + P2C load balancing
- [ ] Request coalescing (singleflight for identical non-streaming requests)

### Deployment
- [x] Single static binary (pure Go, no CGO)
- [x] Docker image
- [x] SQLite with WAL mode (zero external dependencies)
- [ ] PostgreSQL storage backend
- [ ] mTLS support

### Horizontal Scaling (Planned)
- [ ] PostgreSQL backend for shared state (prereq)
- [ ] Redis for centralized rate limits and quota counters
- [ ] Redis response cache backend (replace per-process W-TinyLFU for multi-instance)
- [ ] Stateless hot path (move all mutable state to PG/Redis)
- [ ] Usage recording via shared queue (Redis streams or direct PG writes)
- [ ] Kubernetes-ready: liveness/readiness probes (done), Helm chart, HPA guidance

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

Key sections: `server` (address, timeouts), `database` (SQLite DSN), `providers` (name, type, credentials, models, priority), `routes` (model alias to provider mapping), `rate_limits` (RPM/TPM defaults), `cache` (size, TTL), `keys` (bootstrap API keys with roles).

Provider `name` is the instance identifier (registry key, DB primary key, route target reference). Provider `type` selects the wire format (`openai`, `anthropic`, `gemini`, `ollama`). When `type` is omitted, it defaults to `name` for backward compatibility.

```yaml
providers:
  - name: openai-us          # instance ID
    type: openai              # wire format (defaults to name if omitted)
    base_url: https://api.openai.com/v1
    api_key: "${OPENAI_US_KEY}"
    models: [gpt-4o]

  - name: openai-eu
    type: openai
    base_url: https://eu.api.openai.com/v1
    api_key: "${OPENAI_EU_KEY}"
    models: [gpt-4o]
```

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
  provider/            Registry (keyed by instance name) + adapters (openai, anthropic, gemini, ollama)
  auth/                API key auth with otter cache, per-key roles
  ratelimit/           dual token bucket (RPM+TPM), Registry, QuotaTracker
  circuitbreaker/      per-provider circuit breaker (sliding window, half-open probe)
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
