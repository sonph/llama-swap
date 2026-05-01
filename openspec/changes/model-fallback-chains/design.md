## Context

Requests route to a single model via `proxymanager.go`. When a model fails to start (502) or its process is stopping/shutdown (503), the error is written directly to the client's ResponseWriter — no recovery path exists. The failure modes worth retrying are pre-streaming: they happen before the reverse proxy forwards any data to the client.

llama-swap can run as a pure proxy with no local models — all inference served by peer servers. In that mode there are no `ModelConfig` entries; only a `peers:` block. Fallback chains must therefore be configurable without requiring a `ModelConfig`.

Current call chain for a local model:
```
ProxyManager.handleChatCompletions
  → ProcessGroup.ProxyRequest(modelID, w, r)
    → Process.ProxyRequest(w, r)
      → on 502/503: http.Error(w, ...) — response committed, game over
      → on 200: reverse proxy streams to w
```

## Goals / Non-Goals

**Goals:**
- On 502/503 from primary model, automatically retry next model in fallback list
- Two config locations: top-level `fallbacks:` map (works for peer-only setups) and per-model `fallback:` field in ModelConfig
- No response committed to client until a model succeeds or all exhausted
- Work with local models (process-group and matrix) and peer models
- Minimize diff surface in existing files to reduce upstream merge conflicts

**Non-Goals:**
- Mid-stream retry (once streaming starts, connection is committed)
- Retry on 4xx errors (client-side issues, not model availability)
- Retry on 429 (model is running, just at capacity)
- Health-probing or pre-flight checks before routing

## Decisions

**D1: Two config locations merged at load time into a single lookup**

Top-level `fallbacks:` is a `map[string][]string` keyed by model name (or alias):
```yaml
fallbacks:
  gpt-4o: [local-llama, backup-peer]
```

Per-model `fallback:` in ModelConfig:
```yaml
models:
  local-llama:
    cmd: ...
    fallback: [backup-peer, last-resort]
```

Resolution at load time: model-level `fallback` is merged into the top-level map under the model's ID (model-level wins on conflict). At runtime, routing calls `config.GetFallbackChain(modelID) []string` — one lookup, no branching.

This keeps the runtime path simple and the only diff to `config.go` is adding the `Fallbacks` field and a small merge loop at the end of model processing.

**D2: Intercepting ResponseWriter for failure detection**

Problem: `Process.ProxyRequest` (and `peerproxy.ErrorHandler`) write errors directly to the ResponseWriter, committing the response.

Solution: wrap the real ResponseWriter with an interceptor that buffers status and body. On first write, checks status:
- If retriable (502, 503): buffer small error body, do not flush to client, allow retry
- If non-retriable: flush headers + body to real writer and commit

New file `proxy/fallback_writer.go` — zero changes to `process.go`, `processgroup.go`, `matrix.go`, or `peerproxy.go`.

**D3: Retriable status codes: 502 and 503 only**

- 502 BadGateway = process failed to start, OR peer connection refused (no process at target port — `httputil.ReverseProxy.ErrorHandler` fires and writes 502)
- 503 ServiceUnavailable = process is stopping/shutdown

429 TooManyRequests is NOT retriable — the model is alive but busy.

**D4: Single new method on ProxyManager for fallback routing**

Add `pm.proxyWithFallback(c *gin.Context, modelID string, bodyBytes []byte)` to a new file `proxy/fallback.go`. Each inference handler (`handleChatCompletions`, `handleCompletion`, `handleEmbedding`) replaces its final `nextHandler(...)` call with `pm.proxyWithFallback(...)`. Change to each handler: one line replaced.

The method:
1. Builds the chain: `[modelID] + config.GetFallbackChain(modelID)`
2. Tracks visited set for loop detection
3. For each candidate: resolves handler (matrix/processgroup/peer), wraps writer, calls handler, checks status
4. On success: returns. On retriable error: logs and continues. On exhaustion: flushes last error.

**D5: Loop detection**

Track visited model IDs in a set. Skip any that revisit an already-tried ID. Log a warning.

## Risks / Trade-offs

**Response buffering adds latency on retry path**: intercepting writer overhead is negligible (small error body). Successful responses stream through without full buffering.

**Start latency multiplication**: N fallbacks × M-second startup = N×M worst case. Document in config example.

**Streaming LoadingState incompatible with fallback**: when `sendLoadingState: true`, SSE events may be sent before start failure is known. Fallback still proceeds but client may see duplicate loading events. Known limitation.

## Migration Plan

- All new fields optional — zero behavior change for existing configs
- Rollback: remove `fallbacks:` / `fallback:` from config, redeploy

## Open Questions

(none)
