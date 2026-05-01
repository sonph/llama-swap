## Why

When a model becomes unavailable (start failure, OOM, quota exceeded, 503), requests fail hard with no recovery path. Users need resilience — serve from an alternate model instead of returning errors.

## What Changes

- New `fallback` field in model config: ordered list of model names to try when primary fails
- Routing layer iterates the fallback list on failure, attempting each in sequence
- Returns first successful response or final error if all exhausted
- Fallback triggers on: process start failure (502), process unavailable (503), connection errors

## Capabilities

### New Capabilities
- `model-fallback`: When a requested model fails, automatically retry with next model in a configured fallback list until one succeeds or list is exhausted

### Modified Capabilities

## Impact

- `proxy/config/model_config.go`: new `Fallback []string` field
- `proxy/proxymanager.go`: routing logic to iterate fallback chain
- `proxy/process.go`: may need to surface error types to distinguish retriable vs fatal failures
- Config YAML format: new optional `fallback` key under model definitions
