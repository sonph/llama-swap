package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/event"
)

type Model struct {
	Id           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	State        string   `json:"state"`
	Unlisted     bool     `json:"unlisted"`
	PeerID       string   `json:"peerID"`
	Aliases      []string `json:"aliases,omitempty"`
	ContextSize  string   `json:"contextSize,omitempty"`
	KvCacheTypes []string `json:"kvCacheTypes,omitempty"`
}

// parseCtxSize extracts the context size from a command string like "--ctx-size 100000"
// Returns a human-readable string like "100k" or empty string if not found.
func parseCtxSize(cmd string) string {
	args := strings.Fields(cmd)
	for i, arg := range args {
		if arg == "--ctx-size" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return args[i+1]
			}
			if n >= 1000 {
				return fmt.Sprintf("%dk", n/1000)
			}
			return strconv.Itoa(n)
		}
	}
	return ""
}

// parseKvCacheTypes extracts KV cache types from command flags.
// Supports: --kv-cache-type k:q8_0,v:f16 and --kv-cache-type-k / --kv-cache-type-v
// Returns types like ["q8_0", "f16"] or empty slice.
func parseKvCacheTypes(cmd string) []string {
	args := strings.Fields(cmd)
	var kType, vType string
	for i, arg := range args {
		if arg == "--kv-cache-type" && i+1 < len(args) {
			val := args[i+1]
			parts := strings.Split(val, ",")
			for _, part := range parts {
				kv := strings.SplitN(part, ":", 2)
				if len(kv) == 2 {
					if kv[0] == "k" {
						kType = kv[1]
					} else if kv[0] == "v" {
						vType = kv[1]
					}
				} else if len(kv) == 1 {
					if kType == "" {
						kType = kv[0]
					}
					if vType == "" {
						vType = kv[0]
					}
				}
			}
		} else if arg == "--kv-cache-type-k" && i+1 < len(args) {
			kType = args[i+1]
		} else if arg == "--kv-cache-type-v" && i+1 < len(args) {
			vType = args[i+1]
		}
	}
	var result []string
	if kType != "" {
		result = append(result, kType)
	}
	if vType != "" && vType != kType {
		result = append(result, vType)
	}
	return result
}

func addApiHandlers(pm *ProxyManager) {
	// Add API endpoints for React to consume
	// Protected with API key authentication
	apiGroup := pm.ginEngine.Group("/api", pm.apiKeyAuth())
	{
		apiGroup.POST("/models/unload", pm.apiUnloadAllModels)
		apiGroup.POST("/models/unload/*model", pm.apiUnloadSingleModelHandler)
		apiGroup.GET("/events", pm.apiSendEvents)
		apiGroup.GET("/metrics", pm.apiGetMetrics)
		apiGroup.GET("/version", pm.apiGetVersion)
		apiGroup.GET("/captures/:id", pm.apiGetCapture)
	}
}

func (pm *ProxyManager) apiUnloadAllModels(c *gin.Context) {
	pm.StopProcesses(StopImmediately)
	c.JSON(http.StatusOK, gin.H{"msg": "ok"})
}

func (pm *ProxyManager) getModelStatus() []Model {
	// Extract keys and sort them
	models := []Model{}

	modelIDs := make([]string, 0, len(pm.config.Models))
	for modelID := range pm.config.Models {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)

	// Iterate over sorted keys
	for _, modelID := range modelIDs {
		// Get process state
		state := "unknown"
		var process *Process
		if pm.matrix != nil {
			process, _ = pm.matrix.GetProcess(modelID)
		} else {
			processGroup := pm.findGroupByModelName(modelID)
			if processGroup != nil {
				process = processGroup.processes[modelID]
			}
		}
		if process != nil {
			switch process.CurrentState() {
			case StateReady:
				state = "ready"
			case StateStarting:
				state = "starting"
			case StateStopping:
				state = "stopping"
			case StateShutdown:
				state = "shutdown"
			case StateStopped:
				state = "stopped"
			}
		}
		cmd := pm.config.Models[modelID].Cmd
		models = append(models, Model{
			Id:           modelID,
			Name:         pm.config.Models[modelID].Name,
			Description:  pm.config.Models[modelID].Description,
			State:        state,
			Unlisted:     pm.config.Models[modelID].Unlisted,
			Aliases:      pm.config.Models[modelID].Aliases,
			ContextSize:  parseCtxSize(cmd),
			KvCacheTypes: parseKvCacheTypes(cmd),
		})
	}

	// Iterate over the peer models
	if pm.peerProxy != nil {
		for peerID, peer := range pm.peerProxy.ListPeers() {
			for _, modelID := range peer.Models {
				models = append(models, Model{
					Id:     modelID,
					PeerID: peerID,
				})
			}
		}
	}

	return models
}

type messageType string

const (
	msgTypeModelStatus messageType = "modelStatus"
	msgTypeLogData     messageType = "logData"
	msgTypeMetrics     messageType = "metrics"
	msgTypeInFlight    messageType = "inflight"
)

type messageEnvelope struct {
	Type messageType `json:"type"`
	Data string      `json:"data"`
}

// sends a stream of different message types that happen on the server
func (pm *ProxyManager) apiSendEvents(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	// prevent nginx from buffering SSE
	c.Header("X-Accel-Buffering", "no")

	sendBuffer := make(chan messageEnvelope, 25)
	ctx, cancel := context.WithCancel(c.Request.Context())
	sendModels := func() {
		data, err := json.Marshal(pm.getModelStatus())
		if err == nil {
			msg := messageEnvelope{Type: msgTypeModelStatus, Data: string(data)}
			select {
			case sendBuffer <- msg:
			case <-ctx.Done():
				return
			default:
			}

		}
	}

	sendLogData := func(source string, data []byte) {
		data, err := json.Marshal(gin.H{
			"source": source,
			"data":   string(data),
		})
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeLogData, Data: string(data)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	sendMetrics := func(metrics []ActivityLogEntry) {
		jsonData, err := json.Marshal(metrics)
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeMetrics, Data: string(jsonData)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	sendInFlight := func(total int) {
		jsonData, err := json.Marshal(gin.H{"total": total})
		if err == nil {
			select {
			case sendBuffer <- messageEnvelope{Type: msgTypeInFlight, Data: string(jsonData)}:
			case <-ctx.Done():
				return
			default:
			}
		}
	}

	/**
	 * Send updated models list
	 */
	defer event.On(func(e ProcessStateChangeEvent) {
		sendModels()
	})()
	defer event.On(func(e ConfigFileChangedEvent) {
		sendModels()
	})()

	/**
	 * Send Log data
	 */
	defer pm.proxyLogger.OnLogData(func(data []byte) {
		sendLogData("proxy", data)
	})()
	defer pm.upstreamLogger.OnLogData(func(data []byte) {
		sendLogData("upstream", data)
	})()

	/**
	 * Send Metrics data
	 */
	defer event.On(func(e ActivityLogEvent) {
		sendMetrics([]ActivityLogEntry{e.Metrics})
	})()

	/**
	 * Send in-flight request stats related to token stats "Waiting: N" count.
	 */
	defer event.On(func(e InFlightRequestsEvent) {
		sendInFlight(e.Total)
	})()

	// send initial batch of data
	sendLogData("proxy", pm.proxyLogger.GetHistory())
	sendLogData("upstream", pm.upstreamLogger.GetHistory())
	sendModels()
	sendMetrics(pm.metricsMonitor.getMetrics())
	sendInFlight(pm.inFlightCounter.Current())

	for {
		select {
		case <-c.Request.Context().Done():
			cancel()
			return
		case <-pm.shutdownCtx.Done():
			cancel()
			return
		case msg := <-sendBuffer:
			c.SSEvent("message", msg)
			c.Writer.Flush()
		}
	}
}

func (pm *ProxyManager) apiGetMetrics(c *gin.Context) {
	if c.Query("aggregate") == "true" {
		pm.apiGetAggregatedMetrics(c)
		return
	}

	jsonData, err := pm.metricsMonitor.getMetricsJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get metrics"})
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (pm *ProxyManager) apiGetAggregatedMetrics(c *gin.Context) {
	// Build model states map
	modelStates := make(map[string]string)
	for _, model := range pm.getModelStatus() {
		modelStates[model.Id] = model.State
	}

	agg := pm.metricsMonitor.getAggregatedMetrics(modelStates)

	// Attach aliases from config
	for modelID, mm := range agg.Models {
		if cfg, ok := pm.config.Models[modelID]; ok {
			mm.Aliases = cfg.Aliases
			agg.Models[modelID] = mm
		}
	}

	jsonData, err := json.Marshal(agg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to aggregate metrics"})
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (pm *ProxyManager) apiUnloadSingleModelHandler(c *gin.Context) {
	requestedModel := strings.TrimPrefix(c.Param("model"), "/")
	realModelName, found := pm.config.RealModelName(requestedModel)
	if !found {
		pm.sendErrorResponse(c, http.StatusNotFound, "Model not found")
		return
	}

	var stopErr error
	if pm.matrix != nil {
		stopErr = pm.matrix.StopProcess(realModelName, StopImmediately)
	} else {
		processGroup := pm.findGroupByModelName(realModelName)
		if processGroup == nil {
			pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("process group not found for model %s", requestedModel))
			return
		}
		stopErr = processGroup.StopProcess(realModelName, StopImmediately)
	}

	if stopErr != nil {
		pm.sendErrorResponse(c, http.StatusInternalServerError, fmt.Sprintf("error stopping process: %s", stopErr.Error()))
		return
	}
	c.String(http.StatusOK, "OK")
}

func (pm *ProxyManager) apiGetVersion(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]string{
		"version":    pm.version,
		"commit":     pm.commit,
		"build_date": pm.buildDate,
	})
}

func (pm *ProxyManager) apiGetCapture(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid capture ID"})
		return
	}

	capture := pm.metricsMonitor.getCaptureByID(id)
	if capture == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "capture not found"})
		return
	}

	jsonBytes, err := json.Marshal(capture)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal capture"})
		return
	}
	c.Data(http.StatusOK, "application/json", jsonBytes)
}
