package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
	"github.com/vango-go/vai-lite/pkg/gateway/compat"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/lifecycle"
	"github.com/vango-go/vai-lite/pkg/gateway/limits"
	"github.com/vango-go/vai-lite/pkg/gateway/mw"
	"github.com/vango-go/vai-lite/pkg/gateway/principal"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
	"github.com/vango-go/vai-lite/pkg/gateway/runloop"
	"github.com/vango-go/vai-lite/pkg/gateway/sse"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/exa"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/firecrawl"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/tavily"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
)

// RunsHandler handles /v1/runs and /v1/runs:stream requests.
type RunsHandler struct {
	Config     config.Config
	Upstreams  ProviderFactory
	HTTPClient *http.Client
	Logger     *slog.Logger
	Limiter    *ratelimit.Limiter
	Lifecycle  *lifecycle.Lifecycle
	Stream     bool
}

func (h RunsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reqID, _ := mw.RequestIDFrom(r.Context())
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrInvalidRequest, Message: "method not allowed", Code: "method_not_allowed", RequestID: reqID}, http.StatusMethodNotAllowed)
		return
	}

	reqID, _ := mw.RequestIDFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, h.Config.MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeCoreErrorJSON(w, reqID, core.NewInvalidRequestError("failed to read request body"), http.StatusBadRequest)
		return
	}

	runReq, err := types.UnmarshalRunRequestStrict(body)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	if err := limits.ValidateMessageRequest(&runReq.Request, h.Config); err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	if len(h.Config.ModelAllowlist) > 0 {
		if _, ok := h.Config.ModelAllowlist[runReq.Request.Model]; !ok {
			writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrPermission, Message: "model is not allowlisted", Param: "request.model", RequestID: reqID}, http.StatusForbidden)
			return
		}
	}

	providerName, modelName, err := core.ParseModelString(runReq.Request.Model)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}
	upstreamKeyHeader, ok := compat.ProviderKeyHeader(providerName)
	if !ok {
		writeCoreErrorJSON(w, reqID, core.NewInvalidRequestErrorWithParam("unsupported provider", "request.model"), http.StatusBadRequest)
		return
	}
	upstreamKey := strings.TrimSpace(r.Header.Get(upstreamKeyHeader))
	if upstreamKey == "" {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrAuthentication, Message: "missing upstream provider api key header", Param: upstreamKeyHeader, Code: "provider_key_missing", RequestID: reqID}, http.StatusUnauthorized)
		return
	}

	workingReq := *runReq
	workingReq.Request.Model = modelName

	voicePipeline := h.resolveVoicePipeline(r)
	if workingReq.Request.Voice != nil && voicePipeline == nil {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrAuthentication, Message: "missing voice provider api key header", Param: "X-Provider-Key-Cartesia", RequestID: reqID}, http.StatusUnauthorized)
		return
	}

	effectiveServerTools := workingReq.ServerTools
	if len(effectiveServerTools) == 0 {
		effectiveServerTools = workingReq.Builtins
	}
	registry, err := h.newServerToolsRegistry(r, effectiveServerTools, workingReq.ServerToolConfig)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}
	injectedTools, err := registry.ValidateAndInject(workingReq.Request.Tools, effectiveServerTools)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}
	workingReq.Request.Tools = injectedTools

	if compatIssues := compat.ValidateMessageRequest(&workingReq.Request, providerName, runReq.Request.Model); len(compatIssues) > 0 {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrInvalidRequest, Message: fmt.Sprintf("Request is incompatible with provider %s and model %s", providerName, modelName), CompatIssues: compatIssues, RequestID: reqID}, http.StatusBadRequest)
		return
	}

	provider, err := h.Upstreams.New(providerName, upstreamKey)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	if h.Stream {
		h.serveStream(w, r, reqID, runReq.Request.Model, provider, &workingReq, voicePipeline, registry)
		return
	}

	controller := &runloop.Controller{
		Provider:          provider,
		Tools:             registry,
		VoicePipeline:     voicePipeline,
		StreamIdleTimeout: h.Config.StreamIdleTimeout,
		RequestID:         reqID,
		PublicModel:       runReq.Request.Model,
	}
	result, err := controller.RunBlocking(r.Context(), &workingReq)
	if err != nil {
		// /v1/runs run timeout is modeled as a successful terminal result.
		if !(errors.Is(err, context.DeadlineExceeded) &&
			r.Context().Err() == nil &&
			result != nil &&
			result.StopReason == types.RunStopReasonTimeout) {
			h.writeErr(w, reqID, err, false)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(types.RunResultEnvelope{Result: result})
}

func (h RunsHandler) serveStream(
	w http.ResponseWriter,
	r *http.Request,
	reqID string,
	publicModel string,
	provider core.Provider,
	runReq *types.RunRequest,
	voicePipeline *voice.Pipeline,
	registry *servertools.Registry,
) {
	if h.Lifecycle != nil && h.Lifecycle.IsDraining() {
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrOverloaded, Message: "gateway is draining", Code: "draining", RequestID: reqID}, 529)
		return
	}

	p := principal.Resolve(r, h.Config)
	if h.Limiter != nil && h.Config.LimitMaxConcurrentStreams > 0 {
		dec := h.Limiter.AcquireStream(p.Key, time.Now())
		if !dec.Allowed {
			if dec.RetryAfter > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", dec.RetryAfter))
			}
			writeCoreErrorJSON(w, reqID, core.NewRateLimitError("too many concurrent streams", dec.RetryAfter), http.StatusTooManyRequests)
			return
		}
		if dec.Permit != nil {
			defer dec.Permit.Release()
		}
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sw, err := sse.New(w)
	if err != nil {
		h.writeErr(w, reqID, err, false)
		return
	}

	ctx := r.Context()
	effectiveTimeout := effectiveRunStreamTimeout(runReq.Run.TimeoutMS, h.Config.SSEMaxStreamDuration)
	if effectiveTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, effectiveTimeout)
		defer cancel()
	}

	if h.Config.SSEPingInterval > 0 {
		ticker := time.NewTicker(h.Config.SSEPingInterval)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = sw.Send("ping", types.RunPingEvent{Type: "ping"})
				}
			}
		}()
	}

	controller := &runloop.Controller{
		Provider:          provider,
		Tools:             registry,
		VoicePipeline:     voicePipeline,
		StreamIdleTimeout: h.Config.StreamIdleTimeout,
		RequestID:         reqID,
		PublicModel:       publicModel,
	}

	result, err := controller.RunStream(ctx, runReq, func(event types.RunStreamEvent) error {
		return sw.Send(event.EventType(), event)
	})
	if err == nil {
		return
	}

	if r.Context().Err() != nil {
		return
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if result == nil {
			result = &types.RunResult{StopReason: types.RunStopReasonCancelled, Steps: []types.RunStep{}}
		}
		result.StopReason = types.RunStopReasonCancelled
		if result.Steps == nil {
			result.Steps = []types.RunStep{}
		}
		_ = sw.Send("run_complete", types.RunCompleteEvent{Type: "run_complete", Result: result})
		return
	}
}

func effectiveRunStreamTimeout(runTimeoutMS int, sseMaxDuration time.Duration) time.Duration {
	var runTimeout time.Duration
	if runTimeoutMS > 0 {
		runTimeout = time.Duration(runTimeoutMS) * time.Millisecond
	}
	switch {
	case runTimeout > 0 && sseMaxDuration > 0 && runTimeout < sseMaxDuration:
		return runTimeout
	case runTimeout > 0 && sseMaxDuration > 0:
		return sseMaxDuration
	case runTimeout > 0:
		return runTimeout
	default:
		return sseMaxDuration
	}
}

func (h RunsHandler) resolveVoicePipeline(r *http.Request) *voice.Pipeline {
	cartesiaKey := strings.TrimSpace(r.Header.Get("X-Provider-Key-Cartesia"))
	if cartesiaKey == "" {
		return nil
	}
	voiceHTTPClient := h.HTTPClient
	if voiceHTTPClient == nil {
		voiceHTTPClient = &http.Client{}
	}
	return voice.NewPipelineWithProviders(
		stt.NewCartesiaWithClient(cartesiaKey, voiceHTTPClient),
		tts.NewCartesiaWithClient(cartesiaKey, voiceHTTPClient),
	)
}

func (h RunsHandler) newServerToolsRegistry(r *http.Request, enabled []string, rawConfig map[string]any) (*servertools.Registry, error) {
	enabledSet := make(map[string]struct{}, len(enabled))
	for i, name := range enabled {
		name = strings.TrimSpace(name)
		switch name {
		case servertools.ToolWebSearch, servertools.ToolWebFetch:
		default:
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("unsupported server tool %q", name),
				Param:   fmt.Sprintf("server_tools[%d]", i),
				Code:    "unsupported_server_tool",
			}
		}
		enabledSet[name] = struct{}{}
	}
	for name := range rawConfig {
		if _, ok := enabledSet[name]; !ok {
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("server tool config provided for disabled tool %q", name),
				Param:   "server_tool_config." + name,
				Code:    "run_validation_failed",
			}
		}
	}

	toolHTTPClient := safety.NewRestrictedHTTPClient(h.HTTPClient)
	executors := make([]servertools.Executor, 0, len(enabled))

	if _, ok := enabledSet[servertools.ToolWebSearch]; ok {
		searchConfig, err := servertools.DecodeWebSearchConfig(rawConfig[servertools.ToolWebSearch])
		if err != nil {
			if strings.Contains(err.Error(), "unsupported provider") {
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: err.Error(),
					Param:   "server_tool_config.vai_web_search.provider",
					Code:    "unsupported_tool_provider",
				}
			}
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: err.Error(),
				Param:   "server_tool_config.vai_web_search",
				Code:    "run_validation_failed",
			}
		}

		if searchConfig.Provider == "" {
			hasTavilyKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyTavily)) != ""
			hasExaKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyExa)) != ""
			if hasTavilyKey == hasExaKey {
				msg := "server_tool_config.vai_web_search.provider is required"
				if hasTavilyKey && hasExaKey {
					msg = "ambiguous web search provider; set server_tool_config.vai_web_search.provider"
				}
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: msg,
					Param:   "server_tool_config.vai_web_search.provider",
					Code:    "tool_provider_missing",
				}
			}
			if hasTavilyKey {
				searchConfig.Provider = servertools.ProviderTavily
			} else {
				searchConfig.Provider = servertools.ProviderExa
			}
		}

		searchHeader := servertools.ProviderHeaderName(searchConfig.Provider)
		searchKey := strings.TrimSpace(r.Header.Get(searchHeader))
		if searchKey == "" {
			return nil, &core.Error{
				Type:    core.ErrAuthentication,
				Message: "missing server tool provider key header",
				Param:   searchHeader,
				Code:    "provider_key_missing",
			}
		}
		var tavilyClient *tavily.Client
		var exaClient *exa.Client
		switch searchConfig.Provider {
		case servertools.ProviderTavily:
			tavilyClient = tavily.NewClient(searchKey, h.Config.TavilyBaseURL, toolHTTPClient)
		case servertools.ProviderExa:
			exaClient = exa.NewClient(searchKey, h.Config.ExaBaseURL, toolHTTPClient)
		default:
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("unsupported web search provider %q", searchConfig.Provider),
				Param:   "server_tool_config.vai_web_search.provider",
				Code:    "unsupported_tool_provider",
			}
		}
		executors = append(executors, servertools.NewWebSearchExecutor(searchConfig, tavilyClient, exaClient))
	}

	if _, ok := enabledSet[servertools.ToolWebFetch]; ok {
		fetchConfig, err := servertools.DecodeWebFetchConfig(rawConfig[servertools.ToolWebFetch])
		if err != nil {
			if strings.Contains(err.Error(), "unsupported provider") {
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: err.Error(),
					Param:   "server_tool_config.vai_web_fetch.provider",
					Code:    "unsupported_tool_provider",
				}
			}
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: err.Error(),
				Param:   "server_tool_config.vai_web_fetch",
				Code:    "run_validation_failed",
			}
		}
		if fetchConfig.Provider == "" {
			hasTavilyKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyTavily)) != ""
			hasFirecrawlKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyFirecrawl)) != ""
			if hasTavilyKey == hasFirecrawlKey {
				msg := "server_tool_config.vai_web_fetch.provider is required"
				if hasTavilyKey && hasFirecrawlKey {
					msg = "ambiguous web fetch provider; set server_tool_config.vai_web_fetch.provider"
				}
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: msg,
					Param:   "server_tool_config.vai_web_fetch.provider",
					Code:    "tool_provider_missing",
				}
			}
			if hasTavilyKey {
				fetchConfig.Provider = servertools.ProviderTavily
			} else {
				fetchConfig.Provider = servertools.ProviderFirecrawl
			}
		}
		fetchHeader := servertools.ProviderHeaderName(fetchConfig.Provider)
		fetchKey := strings.TrimSpace(r.Header.Get(fetchHeader))
		if fetchKey == "" {
			return nil, &core.Error{
				Type:    core.ErrAuthentication,
				Message: "missing server tool provider key header",
				Param:   fetchHeader,
				Code:    "provider_key_missing",
			}
		}
		var firecrawlClient *firecrawl.Client
		var tavilyClient *tavily.Client
		switch fetchConfig.Provider {
		case servertools.ProviderFirecrawl:
			firecrawlClient = firecrawl.NewClient(fetchKey, h.Config.FirecrawlBaseURL, toolHTTPClient)
		case servertools.ProviderTavily:
			tavilyClient = tavily.NewClient(fetchKey, h.Config.TavilyBaseURL, toolHTTPClient)
		default:
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("unsupported web fetch provider %q", fetchConfig.Provider),
				Param:   "server_tool_config.vai_web_fetch.provider",
				Code:    "unsupported_tool_provider",
			}
		}
		executors = append(executors, servertools.NewWebFetchExecutor(fetchConfig, firecrawlClient, tavilyClient))
	}

	return servertools.NewRegistry(executors...), nil
}

func (h RunsHandler) writeErr(w http.ResponseWriter, reqID string, err error, isStream bool) {
	coreErr, status := coreErrorFrom(err, reqID)
	if !isStream {
		writeCoreErrorJSON(w, reqID, coreErr, status)
		return
	}

	sw, sseErr := sse.New(w)
	if sseErr == nil {
		_ = sw.Send("error", types.RunErrorEvent{Type: "error", Error: toTypesError(coreErr)})
	}
}
