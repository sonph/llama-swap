package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

// newErrorTestHandler returns an http.Handler that writes the given status code and body.
func newErrorTestHandler(statusCode int, body string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // drain body
		w.WriteHeader(statusCode)
		w.Write([]byte(body)) //nolint:errcheck // best effort
	})
	return mux
}

// injectTestHandlerForModel sets a testHandler on a specific model's process in all process groups.
func injectTestHandlerForModel(pm *ProxyManager, modelID string, handler http.Handler) {
	for _, pg := range pm.processGroups {
		if process, ok := pg.processes[modelID]; ok {
			process.testHandler = handler
		}
	}
}

// setFallback sets the fallback chain for a model, working around Go's map field assignment limitation.
func setFallback(pm *ProxyManager, modelID string, fallback []string) {
	if mc, ok := pm.config.Models[modelID]; ok {
		mc.Fallback = fallback
		pm.config.Models[modelID] = mc
	}
}

func TestProxyManager_FallbackOnStartFailure(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a returns 502, model-b returns 200
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"primary failed"}`))
	injectTestHandlerForModel(proxy, "model-b", newTestHandler("model-b"))

	// Set up fallback chain via config
	setFallback(proxy, "model-a", []string{"model-b"})

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model-b")
	assert.NotContains(t, w.Body.String(), "primary failed")
}

func TestProxyManager_FallbackOnUnavailable(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a returns 503, model-b returns 200
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusServiceUnavailable, `{"error":"unavailable"}`))
	injectTestHandlerForModel(proxy, "model-b", newTestHandler("model-b"))

	setFallback(proxy, "model-a", []string{"model-b"})

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model-b")
}

func TestProxyManager_FallbackAllExhausted(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// Both models return 502
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", newErrorTestHandler(http.StatusBadGateway, `{"error":"b failed"}`))

	setFallback(proxy, "model-a", []string{"model-b"})

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "b failed")
}

func TestProxyManager_FallbackNotTriggeredOn429(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a returns 429, model-b should NOT be called
	fallbackCalled := false
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusTooManyRequests, `{"error":"rate limited"}`))
	injectTestHandlerForModel(proxy, "model-b", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalled = true
		w.Write([]byte("fallback")) //nolint:errcheck // best effort
	}))

	setFallback(proxy, "model-a", []string{"model-b"})

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.False(t, fallbackCalled, "fallback should not be called for 429")
}

func TestProxyManager_FallbackDuplicateSkipped(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a returns 502, model-b returns 200
	// model-a appears again in its own fallback list — should be skipped
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", newTestHandler("model-b"))

	setFallback(proxy, "model-a", []string{"model-a", "model-b"})

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model-b")
}

func TestProxyManager_FallbackPeerUnreachable(t *testing.T) {
	// Create a peer server that returns 502
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"peer failed"}`)) //nolint:errcheck // best effort
	}))
	defer peerServer.Close()

	cfg := testConfigFromYAML(t, fmt.Sprintf(`
healthCheckTimeout: 15
logLevel: error
peers:
  test-peer:
    proxy: %s
    models:
      - peer-model
models:
  local-model:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond local-model
fallbacks:
  peer-model:
    - local-model
`, peerServer.URL))

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)
	injectTestHandlerForModel(proxy, "local-model", newTestHandler("local-model"))

	reqBody := `{"model":"peer-model"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "local-model")
}

func TestProxyManager_FallbackFiltersAppliedPerCandidate(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
    filters:
      stripParams: temperature
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a fails; model-b's handler captures the request body to verify temperature is present
	var receivedBody string
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"responseMessage": "model-b",
		})
	}))

	setFallback(proxy, "model-a", []string{"model-b"})

	// Request includes temperature — model-a would strip it, but model-b should receive it
	reqBody := `{"model":"model-a","temperature":0.7}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// model-b has no stripParams filter, so temperature should be in the body sent to it
	assert.Contains(t, receivedBody, "temperature")
}

func TestProxyManager_TopLevelFallbackNoPeerConfig(t *testing.T) {
	// Peer server that returns 502
	peerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"peer failed"}`)) //nolint:errcheck // best effort
	}))
	defer peerServer.Close()

	// Another peer that succeeds as fallback
	fallbackPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"responseMessage": "fallback-peer",
		})
	}))
	defer fallbackPeer.Close()

	cfg := testConfigFromYAML(t, fmt.Sprintf(`
healthCheckTimeout: 15
logLevel: error
peers:
  primary-peer:
    proxy: %s
    models:
      - gpt-4o
  fallback-peer:
    proxy: %s
    models:
      - local-llama
fallbacks:
  gpt-4o:
    - local-llama
`, peerServer.URL, fallbackPeer.URL))

	proxy := New(cfg)
	defer proxy.StopProcesses(StopImmediately)

	reqBody := `{"model":"gpt-4o"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "fallback-peer")
}

func TestProxyManager_FallbackWithAlias(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
    aliases:
      - alias-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a returns 502, model-b returns 200
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", newTestHandler("model-b"))

	setFallback(proxy, "model-a", []string{"model-b"})

	// Request using the alias
	reqBody := `{"model":"alias-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model-b")
}

func TestProxyManager_FallbackChainOrder(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
  model-c:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-c
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a fails, model-b fails, model-c succeeds
	// This verifies that the fallback chain is tried in order
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", newErrorTestHandler(http.StatusBadGateway, `{"error":"b failed"}`))
	injectTestHandlerForModel(proxy, "model-c", newTestHandler("model-c"))

	setFallback(proxy, "model-a", []string{"model-b", "model-c"})

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model-c")
}

func TestProxyManager_FallbackPreservesOriginalBody(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
    filters:
      setParams:
        temperature: 0.9
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a fails; model-b captures the request body
	var capturedBody string
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		capturedBody = string(bodyBytes)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"responseMessage": "model-b",
		})
	}))

	setFallback(proxy, "model-a", []string{"model-b"})

	// Request with temperature 0.5 — model-a would override to 0.9,
	// but model-b should get the original with its own filters applied (none)
	reqBody := `{"model":"model-a","temperature":0.5}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// model-b has no setParams filter, so it should get the original temperature
	assert.True(t, gjson.Get(capturedBody, "temperature").Float() == 0.5,
		"model-b should receive original temperature 0.5, got: %s", capturedBody)
}

func TestProxyManager_NoFallbackReturnsErrorImmediately(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	// model-a returns 502 with no fallback configured
	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"failed"}`))

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "failed")
}

func TestProxyManager_FallbackOnCompletionEndpoint(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", newTestHandler("model-b"))

	setFallback(proxy, "model-a", []string{"model-b"})

	reqBody := `{"model":"model-a"}`
	req := httptest.NewRequest("POST", "/v1/completions", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model-b")
}

func TestProxyManager_FallbackOnEmbeddingsEndpoint(t *testing.T) {
	cfg := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model-a:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-a
  model-b:
    cmd: {{RESPONDER}} --port ${PORT} --silent --respond model-b
`)

	proxy := New(cfg)
	defer proxy.StopProcesses(StopWaitForInflightRequest)

	injectTestHandlerForModel(proxy, "model-a", newErrorTestHandler(http.StatusBadGateway, `{"error":"a failed"}`))
	injectTestHandlerForModel(proxy, "model-b", newTestHandler("model-b"))

	setFallback(proxy, "model-a", []string{"model-b"})

	reqBody := `{"model":"model-a","input":"test"}`
	req := httptest.NewRequest("POST", "/v1/embeddings", bytes.NewBufferString(reqBody))
	w := CreateTestResponseRecorder()

	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model-b")
}
