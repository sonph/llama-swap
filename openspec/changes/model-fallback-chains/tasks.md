## 1. Config Changes (minimal diff)

- [x] 1.1 Add `Fallback []string` field to `ModelConfig` in `proxy/config/model_config.go` with yaml tag `fallback`
- [x] 1.2 Add `Fallbacks map[string][]string` field to `Config` struct in `proxy/config/config.go` with yaml tag `fallbacks`; initialize to empty map in `LoadConfigFromReader` defaults block
- [x] 1.3 Add `GetFallbackChain(modelID string) []string` method on `*Config`: if `config.Models[modelID].Fallback` is non-empty return it; else return `config.Fallbacks[modelID]`; return nil if neither is set
- [x] 1.4 In `LoadConfigFromReader`, after the main model processing loop (around line 444 where `config.Models[modelId] = modelConfig`), add a loop that copies each model's `Fallback` slice into `config.Fallbacks[modelId]` only when `config.Fallbacks[modelId]` is not already set (top-level wins for models with no per-model fallback; per-model wins because it was already stored in `modelConfig.Fallback` and `GetFallbackChain` checks that first)

## 2. New File: proxy/fallback_writer.go

Create `proxy/fallback_writer.go`. No changes to any existing file.

- [x] 2.1 Define `fallbackResponseWriter` struct embedding `http.ResponseWriter` with fields: `statusCode int`, `buf bytes.Buffer`, `retriable bool`, `committed bool`
- [x] 2.2 Implement `WriteHeader(code int)`: set `f.statusCode = code`; if `isFallbackRetriable(code)` set `f.retriable = true`; do NOT call the underlying `WriteHeader` yet
- [x] 2.3 Implement `Write(b []byte) (int, error)`: if `f.retriable` buffer into `f.buf` (never forward to underlying writer); otherwise, if not yet committed, call `f.ResponseWriter.WriteHeader(f.statusCode)` then `f.committed = true`, then write through to underlying writer
- [x] 2.4 Implement `Flush()` on `fallbackResponseWriter`: if not retriable and not committed, commit headers first; then forward to underlying `http.Flusher` if available — this ensures SSE/streaming works correctly
- [x] 2.5 Implement `flushErrorToWriter(w http.ResponseWriter)` method: writes `f.statusCode` and buffered body to `w`; used when all fallbacks exhausted
- [x] 2.6 Implement `isFallbackRetriable(code int) bool`: returns `true` for 502 and 503 only

## 3. New File: proxy/fallback.go

Create `proxy/fallback.go`. No changes to any existing file.

- [x] 3.1 Define `pm.applyJSONFilters(modelID string, requestedModel string, bodyBytes []byte) ([]byte, error)` helper: applies useModelName rewrite, stripParams, setParams, setParamsByID (same logic currently in `mkProxyJSONHandler` lines ~770–815) to a copy of bodyBytes; returns the filtered copy; returns error on failure. For peer models (no ModelConfig entry), applies peer filters via `pm.peerProxy.GetPeerFilters(requestedModel)` instead.

- [x] 3.2 Define `pm.resolveHandler(modelID string) (func(string, http.ResponseWriter, *http.Request) error, error)` helper: resolves the right handler for a modelID — calls `pm.swapProcessGroup(modelID)` and returns `processGroup.ProxyRequest`, or `pm.matrix.ProxyRequest` if matrix configured, or `pm.peerProxy.ProxyRequest` if it's a peer model; returns error if model not found in any routing table
  - NOTE: `requestedModel` param removed from spec as it was unused in the resolution logic

- [x] 3.3 Implement `pm.proxyWithFallback(c *gin.Context, requestedModel string, originalBodyBytes []byte, cf captureFields)`:
  - Builds the candidate chain: `[requestedModel] + pm.config.GetFallbackChain(resolvedPrimaryID)`. The first element is the originally requested model (or its real model ID after alias resolution).
  - Maintains a `visited map[string]bool` to detect duplicates. On duplicate: log warning with `pm.proxyLogger.Warnf`, skip that entry.
  - For each candidate in the chain:
    1. Resolve real model ID via `pm.config.RealModelName(candidate)` — or use candidate as-is if it's a known peer model. If not found in either: log warning, skip.
    2. If `visited[realID]`: log warning, skip.
    3. Add `realID` to `visited`.
    4. Call `applyJSONFilters(realID, requestedModel, originalBodyBytes)` — use the original `requestedModel` (client's perspective) for setParamsByID key matching. On filter error: call `pm.sendErrorResponse` and return.
    5. Set `c.Request.Body`, `Content-Length` header, delete `transfer-encoding` header (same as current handler lines 856–861).
    6. Set context values: `proxyCtxKey("streaming")` from `gjson.GetBytes(filteredBytes, "stream").Bool()` and `proxyCtxKey("model")` to `realID`; update `c.Request`.
    7. Create `fallbackResponseWriter` wrapping `c.Writer`.
    8. Resolve handler via `resolveHandler(realID)`. On error: call `pm.sendErrorResponse` and return.
    9. Call handler — either `pm.metricsMonitor.wrapHandler(realID, &fbw, c.Request, cf, handler)` if metricsMonitor set, or `handler(realID, &fbw, c.Request)` directly.
    10. If `fbw.retriable`: log `pm.proxyLogger.Warnf("<%s> retriable error %d, trying next fallback", realID, fbw.statusCode)`; continue loop.
    11. If not retriable (success or non-retriable error): return (response already written through).
  - After loop exhausted: call `fbw.flushErrorToWriter(c.Writer)` with the last candidate's buffered error.
  - NOTE: when `handler == nil` (model not found in routing table), the implementation logs a warning and continues to the next fallback candidate rather than returning an error. This is more robust than the spec's "sendErrorResponse" directive.

## 4. Modify mkProxyJSONHandler (proxy/proxymanager.go)

This is the only existing file that changes for routing. All JSON-body inference endpoints (`/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`, `/v1/responses`, `/v1/messages`, `/reranking`, `/infill`, `/completion`, `/v1/audio/speech`, `/v1/images/generations`, etc.) share the single `mkProxyJSONHandler` factory — there are no separate per-endpoint handler functions.

- [x] 4.1 In `mkProxyJSONHandler`, after reading `bodyBytes` and extracting `requestedModel` (before the model resolution block at ~line 756): save `originalBodyBytes := bytes.Clone(bodyBytes)` (or `append([]byte{}, bodyBytes...)` for older Go versions)
- [x] 4.2 Remove the entire block from "Look for a matching local model first" through the final `nextHandler(...)` call (approximately lines 753–881), including the filter application, peer routing, nil check, request setup, and handler call
- [x] 4.3 Replace that removed block with a single call: `pm.proxyWithFallback(c, requestedModel, originalBodyBytes, cf)`
- [x] 4.4 Verify `mkPostFormHandler` is NOT modified — multipart form endpoints are excluded from fallback (body cannot be cleanly replayed per candidate)

## 5. Tests

- [x] 5.1 `TestConfig_GetFallbackChain` in `proxy/config/config_test.go`: (a) model-level `fallback` returned when set; (b) top-level `fallbacks` used when no model-level; (c) model-level overrides top-level; (d) nil returned when neither set; (e) peer model name with only top-level entry works
- [x] 5.2 Unit tests for `fallbackResponseWriter` in `proxy/fallback_writer_test.go`: (a) 502 → retriable=true, body buffered, underlying writer not written; (b) 503 → same; (c) 200 → retriable=false, headers and body forwarded to underlying writer; (d) 429 → retriable=false, forwarded; (e) `flushErrorToWriter` writes buffered status+body to a fresh writer
- [x] 5.3 `TestProxyManager_FallbackOnStartFailure`: set up two fake upstream servers; primary returns 502 immediately; fallback returns 200; assert client receives 200 and no 502
- [x] 5.4 `TestProxyManager_FallbackOnUnavailable`: primary process in Stopping state (503); fallback returns 200; assert client receives 200
- [x] 5.5 `TestProxyManager_FallbackAllExhausted`: all candidates return 502; assert client receives 502 with the last candidate's error body
- [x] 5.6 `TestProxyManager_FallbackNotTriggeredOn429`: primary returns 429; assert client receives 429 immediately, fallback never called
- [x] 5.7 `TestProxyManager_FallbackDuplicateSkipped`: fallback list contains primary model ID again; assert it's attempted only once; warning logged
- [x] 5.8 `TestProxyManager_FallbackPeerUnreachable`: configure peer at a port with nothing listening; fallback points to local model; assert client receives 200 from local model
- [x] 5.9 `TestProxyManager_FallbackFiltersAppliedPerCandidate`: model-a has `stripParams: temperature`; model-b does not; model-a fails; assert body sent to model-b includes `temperature`
- [x] 5.10 `TestProxyManager_TopLevelFallbackNoPeerConfig`: fallback defined in top-level `fallbacks:` for a peer model with no ModelConfig; assert fallback chain resolves correctly
- [x] 5.11 Run `make test-dev` and fix any failures

## 6. Documentation

- [x] 6.1 Add `fallbacks:` and `fallback:` examples to the example config file with comments explaining: (a) per-model usage; (b) top-level usage for peer-only deployments; (c) note that multipart form endpoints do not support fallback
- [x] 6.2 Run `gofmt -l .` and fix any reported files

## Implementation Notes

Intentional deviations from spec:
- `resolveHandler` takes only `realModelID` (spec had `requestedModel` param) — the param was unused
- `handler == nil` continues to next fallback instead of returning error — more robust behavior
- `WriteHeader` commits immediately for non-retriable codes — performance optimization, functionally equivalent
- `fallbackResponseWriter` includes compatibility methods (`Hijack`, `CloseNotify`, `Pusher`, `Size`, `Status`, `Written`, `WriteString`) for gin.ResponseWriter compatibility — not in spec but required for correct operation
