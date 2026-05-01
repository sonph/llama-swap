package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// applyJSONFilters applies model-specific JSON filters (useModelName, stripParams,
// setParams, setParamsByID) to a copy of the original request body.
// For local models, filters come from the ModelConfig. For peer models, filters
// come from the peer proxy configuration.
func (pm *ProxyManager) applyJSONFilters(realModelID, requestedModel string, bodyBytes []byte) ([]byte, error) {
	filtered := bytes.Clone(bodyBytes)

	// Check if this is a local model with a ModelConfig entry
	if modelConfig, found := pm.config.Models[realModelID]; found {
		// Apply useModelName rewrite
		if modelConfig.UseModelName != "" {
			var err error
			filtered, err = sjson.SetBytes(filtered, "model", modelConfig.UseModelName)
			if err != nil {
				return nil, err
			}
		}

		// Apply stripParams
		stripParams, err := modelConfig.Filters.SanitizedStripParams()
		if err != nil {
			pm.proxyLogger.Errorf("Error sanitizing strip params string: %s, %s", modelConfig.Filters.StripParams, err.Error())
		} else {
			for _, param := range stripParams {
				var err error
				filtered, err = sjson.DeleteBytes(filtered, param)
				if err != nil {
					return nil, err
				}
			}
		}

		// Apply setParams
		setParams, setParamKeys := modelConfig.Filters.SanitizedSetParams()
		for _, key := range setParamKeys {
			var err error
			filtered, err = sjson.SetBytes(filtered, key, setParams[key])
			if err != nil {
				return nil, err
			}
		}

		// Apply setParamsByID
		setParamsByIDParams, setParamsByIDKeys := modelConfig.Filters.SanitizedSetParamsByID(requestedModel)
		for _, key := range setParamsByIDKeys {
			var err error
			filtered, err = sjson.SetBytes(filtered, key, setParamsByIDParams[key])
			if err != nil {
				return nil, err
			}
		}

		return filtered, nil
	}

	// Peer model — apply peer filters
	if pm.peerProxy != nil {
		peerFilters := pm.peerProxy.GetPeerFilters(requestedModel)

		stripParams := peerFilters.SanitizedStripParams()
		for _, param := range stripParams {
			var err error
			filtered, err = sjson.DeleteBytes(filtered, param)
			if err != nil {
				return nil, err
			}
		}

		setParams, setParamKeys := peerFilters.SanitizedSetParams()
		for _, key := range setParamKeys {
			var err error
			filtered, err = sjson.SetBytes(filtered, key, setParams[key])
			if err != nil {
				return nil, err
			}
		}
	}

	return filtered, nil
}

// resolveHandler determines the correct request handler for a model ID.
// Returns a function that proxies requests to the model's process (local, matrix, or peer).
func (pm *ProxyManager) resolveHandler(realModelID string) (func(string, http.ResponseWriter, *http.Request) error, error) {
	// Local model with ModelConfig
	if _, found := pm.config.Models[realModelID]; found {
		if pm.matrix != nil {
			return pm.matrix.ProxyRequest, nil
		}
		processGroup, err := pm.swapProcessGroup(realModelID)
		if err != nil {
			return nil, err
		}
		return processGroup.ProxyRequest, nil
	}

	// Peer model
	if pm.peerProxy != nil && pm.peerProxy.HasPeerModel(realModelID) {
		return pm.peerProxy.ProxyRequest, nil
	}

	return nil, nil // model not found in any routing table
}

// proxyWithFallback routes a request through a chain of candidate models,
// trying each in order until one succeeds or all are exhausted.
// The chain is: [requestedModel] + fallback chain from config.
func (pm *ProxyManager) proxyWithFallback(c *gin.Context, requestedModel string, originalBodyBytes []byte, cf captureFields) {
	// Build the candidate chain: primary model + configured fallbacks
	// First, resolve the primary model's real ID
	var primaryModelID string

	if realID, found := pm.config.RealModelName(requestedModel); found {
		primaryModelID = realID
	} else if pm.peerProxy != nil && pm.peerProxy.HasPeerModel(requestedModel) {
		primaryModelID = requestedModel
	} else {
		pm.sendErrorResponse(c, http.StatusBadRequest, "could not find model "+requestedModel)
		return
	}

	// Build candidate list: primary + fallback chain
	candidates := []string{primaryModelID}
	if chain := pm.config.GetFallbackChain(primaryModelID); chain != nil {
		candidates = append(candidates, chain...)
	}

	visited := make(map[string]bool)
	var lastWriter *fallbackResponseWriter

	for _, candidate := range candidates {
		// Resolve real model ID (handle aliases)
		var realID string
		if real, found := pm.config.RealModelName(candidate); found {
			realID = real
		} else if pm.peerProxy != nil && pm.peerProxy.HasPeerModel(candidate) {
			realID = candidate
		} else {
			pm.proxyLogger.Warnf("<%s> fallback candidate not found in config or peers, skipping", candidate)
			continue
		}

		// Skip duplicates
		if visited[realID] {
			pm.proxyLogger.Warnf("<%s> already attempted in fallback chain, skipping", realID)
			continue
		}
		visited[realID] = true

		// Apply filters for this candidate
		filteredBytes, err := pm.applyJSONFilters(realID, requestedModel, originalBodyBytes)
		if err != nil {
			pm.sendErrorResponse(c, http.StatusInternalServerError, "error applying model filters: "+err.Error())
			return
		}

		// Reconstruct request body
		c.Request.Body = io.NopCloser(bytes.NewBuffer(filteredBytes))
		c.Request.Header.Del("transfer-encoding")
		c.Request.Header.Set("content-length", strconv.Itoa(len(filteredBytes)))
		c.Request.ContentLength = int64(len(filteredBytes))

		// Set context values
		isStreaming := gjson.GetBytes(filteredBytes, "stream").Bool()
		ctx := context.WithValue(c.Request.Context(), proxyCtxKey("streaming"), isStreaming)
		ctx = context.WithValue(ctx, proxyCtxKey("model"), realID)
		c.Request = c.Request.WithContext(ctx)

		// Create intercepting response writer
		fbw := newFallbackResponseWriter(c.Writer)

		// Resolve handler for this candidate
		handler, err := pm.resolveHandler(realID)
		if err != nil {
			pm.sendErrorResponse(c, http.StatusInternalServerError, "error resolving handler: "+err.Error())
			return
		}
		if handler == nil {
			pm.proxyLogger.Warnf("<%s> no handler found for fallback candidate, skipping", realID)
			continue
		}

		// Call handler
		if pm.metricsMonitor != nil && c.Request.Method == "POST" {
			err = pm.metricsMonitor.wrapHandler(realID, fbw, c.Request, cf, handler)
		} else {
			err = handler(realID, fbw, c.Request)
		}
		if err != nil {
			pm.sendErrorResponse(c, http.StatusInternalServerError, "error proxying request: "+err.Error())
			return
		}

		// Check if retriable — if so, try next fallback
		if fbw.retriable {
			pm.proxyLogger.Warnf("<%s> retriable error %d, trying next fallback", realID, fbw.statusCode)
			lastWriter = fbw
			continue
		}

		// Non-retriable (success or client error) — response already written through
		return
	}

	// All candidates exhausted — flush the last error to the client
	if lastWriter != nil {
		lastWriter.flushErrorToWriter(c.Writer)
	}
}
