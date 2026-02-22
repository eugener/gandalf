# Gandalf

LLM gateway that sits between your applications and LLM providers (OpenAI, Anthropic, etc.), adding authentication, routing, and usage tracking.

Phase 1 supports non-streaming `POST /v1/chat/completions` via OpenAI adapter with API key auth.

## Quick start

```bash
# Set required env vars
export OPENAI_API_KEY="sk-..."
export GANDALF_ADMIN_KEY="gnd_your_admin_key"

# Build and run
make run
```

The server starts on `:8080` by default. Send requests using the OpenAI-compatible API:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GANDALF_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/v1/chat/completions` | Required | Chat completion (OpenAI-compatible) |
| GET | `/healthz` | No | Liveness probe |
| GET | `/readyz` | No | Readiness probe |

## Configuration

Gandalf is configured via YAML with `${ENV_VAR}` expansion. See [`configs/gandalf.yaml`](configs/gandalf.yaml) for the full example.

```bash
./bin/gandalf -config configs/gandalf.yaml
```

Key sections:

- **server** -- listen address, timeouts
- **database** -- SQLite DSN
- **providers** -- LLM provider credentials and models
- **routes** -- model alias to provider/model mapping with priority strategy
- **keys** -- bootstrap API keys with org/role assignment

## Development

```bash
make build      # compile binary
make test       # tests with race detector
make bench      # benchmarks with alloc counts
make lint       # go vet + golangci-lint
make check      # full pipeline: build + fix + vet + test + govulncheck
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
cmd/gandalf        -- entrypoint, dependency wiring, graceful shutdown
internal/
  gateway.go       -- domain types + interfaces (no project imports)
  server/          -- HTTP handlers + middleware (chi)
  app/             -- business logic (proxy, routing, key management)
  auth/            -- API key auth with in-memory cache
  provider/        -- provider registry + OpenAI adapter
  storage/sqlite/  -- SQLite with WAL mode, read/write pools, goose migrations
  config/          -- YAML config loading + DB bootstrap
```

## License

[GPL-3.0](LICENSE)
