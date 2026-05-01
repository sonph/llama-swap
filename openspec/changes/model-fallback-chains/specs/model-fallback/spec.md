## ADDED Requirements

### Requirement: Fallback list configuration
Fallback chains SHALL be configurable in two places: a top-level `fallbacks:` map (works for any model name including peer-only setups with no local model configs), and a per-model `fallback:` field inside a ModelConfig (convenience shorthand). Both are optional. Model-level `fallback:` takes precedence when both specify a chain for the same model.

#### Scenario: Top-level fallbacks map — no ModelConfig required
- **WHEN** the config includes `fallbacks: {gpt-4o: [local-llama, backup-peer]}`
- **THEN** requests for gpt-4o fall back to local-llama then backup-peer on retriable failure
- **THEN** no ModelConfig entry for gpt-4o is required (works in peer-only deployments)

#### Scenario: Per-model fallback field
- **WHEN** a model config includes `fallback: [model-b, model-c]`
- **THEN** the system recognizes model-b and model-c as ordered fallback candidates for that model

#### Scenario: Model-level overrides top-level
- **WHEN** both `fallbacks: {model-a: [x]}` and model-a's ModelConfig has `fallback: [y]` are set
- **THEN** the model-level list `[y]` is used; the top-level entry for model-a is ignored

#### Scenario: No fallback defined
- **WHEN** neither `fallbacks:` nor `fallback:` is set for a model
- **THEN** behavior is identical to current behavior — error returned immediately on failure, no retry

### Requirement: Trigger fallback on retriable errors
The system SHALL attempt the next fallback model when the current model's handler writes HTTP 502 (BadGateway) or HTTP 503 (ServiceUnavailable) to the response writer.

#### Scenario: Primary local model fails to start (502)
- **WHEN** a request targets model-a and model-a's process fails to start
- **THEN** the system attempts to serve the request from the first entry in the fallback list
- **THEN** the client does NOT receive the 502 response from model-a

#### Scenario: Primary local model process is stopping (503)
- **WHEN** a request targets model-a and model-a's process state is Stopping or Shutdown
- **THEN** the system attempts to serve the request from the first entry in the fallback list
- **THEN** the client does NOT receive the 503 response from model-a

#### Scenario: Peer model unreachable — connection refused (502)
- **WHEN** a request targets a peer model and no process is running at that peer's port
- **THEN** the peer's `httputil.ReverseProxy.ErrorHandler` writes HTTP 502
- **THEN** the system treats this as retriable and attempts the next fallback model

#### Scenario: Non-retriable error is not retried
- **WHEN** a model returns HTTP 400 or HTTP 429
- **THEN** the system does NOT attempt fallback
- **THEN** the client receives the original error response immediately

### Requirement: Flat fallback chain — primary model's list only
The fallback chain is determined solely by the primary (originally requested) model's configured list. Fallback models' own fallback lists are NOT consulted. The chain is: `[primary] + GetFallbackChain(primary)` — flat, not recursive.

#### Scenario: Chain is determined by primary model only
- **WHEN** model-a has `fallback: [model-b, model-c]` and model-b also has `fallback: [model-d]`
- **WHEN** model-a and model-b both fail
- **THEN** model-c is attempted next (model-a's list continues)
- **THEN** model-d (model-b's fallback) is NOT consulted

#### Scenario: Ordered iteration — first success wins
- **WHEN** model-a fails and model-b succeeds
- **THEN** the client receives model-b's response
- **THEN** model-c (next in model-a's list) is never attempted

#### Scenario: All fallbacks exhausted
- **WHEN** model-a, model-b, and model-c all return retriable errors
- **THEN** the client receives the last model's error response (model-c's)

#### Scenario: Fallback candidate resolves aliases
- **WHEN** a fallback list entry is an alias for a real model ID
- **THEN** the alias is resolved to the real model ID before routing

### Requirement: Per-candidate filter and body application
Each fallback candidate SHALL have its own model filters (useModelName, stripParams, setParams, setParamsByID) applied independently to the original request body. The original request body bytes (before any per-model filter mutations) MUST be preserved and reused for each attempt.

#### Scenario: Filters are not carried over between candidates
- **WHEN** model-a has `stripParams: temperature` and model-b does not
- **WHEN** model-a fails and model-b is attempted
- **THEN** the request body sent to model-b includes `temperature` (model-a's strip filter is not applied to model-b's attempt)

#### Scenario: useModelName applied per candidate
- **WHEN** model-b has `useModelName: llama-3.2`
- **WHEN** the system falls back to model-b
- **THEN** the `model` field in the JSON body sent to model-b is rewritten to `llama-3.2`

### Requirement: No double-commit before a candidate succeeds
The system SHALL NOT write any response to the client until a candidate succeeds or all candidates in the chain are exhausted.

#### Scenario: Error response buffered while next candidate is tried
- **WHEN** model-a fails and model-b is being attempted
- **THEN** model-a's error response body is held in a buffer and not forwarded to the client

#### Scenario: Successful response streams to client
- **WHEN** a candidate model succeeds with a 200 response
- **THEN** the response (including streaming chunks) flows to the client without additional buffering or delay

#### Scenario: Last error flushed when all exhausted
- **WHEN** all candidates in the chain return retriable errors
- **THEN** the final candidate's buffered error response is flushed to the client

### Requirement: Duplicate detection in fallback chain
The system SHALL skip any fallback candidate that has already been attempted in the current request's chain, to prevent retrying the same model twice (e.g. if the primary model appears again in its own fallback list, or if the list contains duplicates).

#### Scenario: Primary model appears in its own fallback list
- **WHEN** model-a has `fallback: [model-b, model-a, model-c]`
- **THEN** model-a is skipped when encountered the second time
- **THEN** a warning is logged identifying the skipped duplicate
- **THEN** model-c is still attempted

#### Scenario: Duplicate within fallback list
- **WHEN** model-a has `fallback: [model-b, model-b]`
- **THEN** model-b is only attempted once; the duplicate entry is skipped with a warning

### Requirement: Fallback applies to JSON-body inference endpoints only
Fallback behavior SHALL apply to all endpoints that use JSON request bodies for model routing (chat completions, completions, responses, embeddings, reranking, audio speech, image generation, etc.). Multipart form endpoints (`/v1/audio/transcriptions`, `/v1/images/edits`) are excluded from fallback because the parsed form body cannot be cleanly replayed per candidate.

#### Scenario: Fallback on /v1/chat/completions
- **WHEN** a POST /v1/chat/completions request targets a failing model with a fallback configured
- **THEN** the request is retried on the fallback model

#### Scenario: Fallback on /v1/completions
- **WHEN** a POST /v1/completions request targets a failing model with a fallback configured
- **THEN** the request is retried on the fallback model

#### Scenario: Fallback on /v1/embeddings
- **WHEN** a POST /v1/embeddings request targets a failing model with a fallback configured
- **THEN** the request is retried on the fallback model

#### Scenario: No fallback on multipart form endpoints
- **WHEN** a POST /v1/audio/transcriptions request targets a failing model with a fallback configured
- **THEN** the error is returned immediately with no fallback attempt
