# Performance and Testing

## Performance Optimizations

- **Route target caching**: otter W-TinyLFU cache (10s TTL) in RouterService eliminates per-request json.Unmarshal of route targets. Saved ~12 allocs/op.
- **Bundled request context**: single `requestMeta` struct holds both requestID and identity. authenticate middleware mutates the existing pointer instead of creating a new context.WithValue + Request.WithContext. Saved 2 allocs/op.
- **GOEXPERIMENT=jsonv2**: encoding/json/v2 reduces allocs in JSON decode/encode. Saves ~1-8 allocs/op depending on path. Set globally in Makefile.
- **Pre-allocated byte slices**: SSE framing (`data: `, `\n\n`, `[DONE]`), Content-Type headers avoid per-request allocations.
- **sync.Pool for statusWriter**: middleware reuses status-capturing wrappers.
- **Direct header map access**: skips MIME canonicalization (1 alloc/req saved per header).

## Benchmark Baseline

With `GOEXPERIMENT=jsonv2` (set in Makefile):

```
ChatCompletion:       ~41 allocs/op  ~4.6us
ChatCompletionStream: ~44 allocs/op  ~4.7us
Healthz:              ~25 allocs/op  ~2.3us
```

Without jsonv2: ChatCompletion ~55, Stream ~60.

Key: avoid generics on hot paths (Go shape dictionary + closure = +1 alloc/op). Use concrete `any` parameter or inline loops instead.

CPU profile shows 96% of time in runtime (GC + scheduling), only 4% in application code. Alloc reduction IS the throughput optimization.

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
