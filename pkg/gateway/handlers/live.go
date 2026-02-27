package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
	"github.com/vango-go/vai-lite/pkg/gateway/compat"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/lifecycle"
	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
	"github.com/vango-go/vai-lite/pkg/gateway/live/session"
	"github.com/vango-go/vai-lite/pkg/gateway/live/sessions"
	"github.com/vango-go/vai-lite/pkg/gateway/mw"
	"github.com/vango-go/vai-lite/pkg/gateway/principal"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/exa"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/firecrawl"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/tavily"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
)

type LiveSTTFactory func(apiKey string, httpClient *http.Client) session.STTProvider

type LiveTTSFactory func(apiKey string, httpClient *http.Client) session.TTSProvider

// LiveHandler handles /v1/live websocket sessions.
type LiveHandler struct {
	Config       config.Config
	Upstreams    ProviderFactory
	HTTPClient   *http.Client
	Logger       *slog.Logger
	Limiter      *ratelimit.Limiter
	Lifecycle    *lifecycle.Lifecycle
	LiveSessions *sessions.Tracker

	NewSTTProvider LiveSTTFactory
	NewTTSProvider LiveTTSFactory
}

func (h LiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		reqID, _ := mw.RequestIDFrom(r.Context())
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrInvalidRequest, Message: "method not allowed", Code: "method_not_allowed", RequestID: reqID}, http.StatusMethodNotAllowed)
		return
	}
	if h.Lifecycle != nil && h.Lifecycle.IsDraining() {
		reqID, _ := mw.RequestIDFrom(r.Context())
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrOverloaded, Message: "gateway is draining", Code: "draining", RequestID: reqID}, 529)
		return
	}
	if !h.originAllowed(r) {
		reqID, _ := mw.RequestIDFrom(r.Context())
		writeCoreErrorJSON(w, reqID, &core.Error{Type: core.ErrPermission, Message: "origin is not allowed", Param: "Origin", RequestID: reqID}, http.StatusForbidden)
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	if h.Config.LiveMaxJSONMessageBytes > 0 {
		conn.SetReadLimit(h.Config.LiveMaxJSONMessageBytes)
	}

	handshakeTimeout := h.Config.LiveHandshakeTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 5 * time.Second
	}
	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	messageType, firstFrame, err := conn.ReadMessage()
	if err != nil {
		h.writeWSError(conn, "session", "bad_request", "failed to read hello", true, nil)
		return
	}
	if messageType != websocket.TextMessage {
		h.writeWSError(conn, "session", "bad_request", "first frame must be hello", true, nil)
		return
	}

	decoded, err := protocol.DecodeClientMessage(firstFrame)
	if err != nil {
		h.writeWSError(conn, "session", "bad_request", "invalid hello frame", true, nil)
		return
	}
	hello, ok := decoded.(protocol.ClientHello)
	if !ok {
		h.writeWSError(conn, "session", "bad_request", "first frame must be hello", true, nil)
		return
	}
	if strings.TrimSpace(hello.ProtocolVersion) != protocol.ProtocolVersion1 {
		h.writeWSError(conn, "session", "unsupported_version", "unsupported protocol_version", true, nil)
		return
	}
	if strings.TrimSpace(hello.AudioIn.Encoding) != "pcm_s16le" || hello.AudioIn.SampleRateHz != 16000 || hello.AudioIn.Channels != 1 {
		h.writeWSError(conn, "session", "unsupported", "audio_in must be pcm_s16le @16000Hz mono", true, nil)
		return
	}
	if strings.TrimSpace(hello.AudioOut.Encoding) != "pcm_s16le" || hello.AudioOut.SampleRateHz != 24000 || hello.AudioOut.Channels != 1 {
		h.writeWSError(conn, "session", "unsupported", "audio_out must be pcm_s16le @24000Hz mono", true, nil)
		return
	}
	if hello.Voice == nil || strings.TrimSpace(hello.Voice.VoiceID) == "" {
		h.writeWSError(conn, "session", "bad_request", "voice.voice_id is required", true, nil)
		return
	}
	voiceProvider := strings.ToLower(strings.TrimSpace(hello.Voice.Provider))
	if voiceProvider == "" {
		h.writeWSError(conn, "session", "bad_request", "voice.provider is required", true, map[string]any{"param": "voice.provider"})
		return
	}
	if voiceProvider != protocol.VoiceProviderCartesia && voiceProvider != protocol.VoiceProviderElevenLabs {
		h.writeWSError(conn, "session", "unsupported", "unsupported voice provider", true, map[string]any{"param": "voice.provider"})
		return
	}
	if voiceProvider == protocol.VoiceProviderElevenLabs && !hello.Features.SendPlaybackMarks {
		h.writeWSError(conn, "session", "bad_request", "send_playback_marks must be true when voice.provider is elevenlabs", true, map[string]any{"param": "features.send_playback_marks"})
		return
	}

	transport := strings.TrimSpace(hello.Features.AudioTransport)
	if transport == "" {
		transport = protocol.AudioTransportBase64JSON
	}
	if transport != protocol.AudioTransportBase64JSON && transport != protocol.AudioTransportBinary {
		h.writeWSError(conn, "session", "unsupported", "unsupported audio transport", true, nil)
		return
	}
	hello.Features.AudioTransport = transport

	apiKey := h.resolveGatewayKey(r, hello)
	principalKey, authErr := h.resolvePrincipal(r, apiKey)
	if authErr != nil {
		h.writeWSError(conn, "session", "unauthorized", authErr.Error(), true, nil)
		return
	}

	var wsPermit *ratelimit.Permit
	if h.Limiter != nil && h.Config.WSMaxSessionsPerPrincipal > 0 {
		dec := h.Limiter.AcquireWSSession(principalKey, time.Now())
		if !dec.Allowed {
			h.writeWSError(conn, "session", "rate_limited", "too many active live sessions", true, nil)
			return
		}
		wsPermit = dec.Permit
		defer wsPermit.Release()
	}

	if len(h.Config.ModelAllowlist) > 0 {
		if _, ok := h.Config.ModelAllowlist[hello.Model]; !ok {
			h.writeWSError(conn, "session", "forbidden", "model is not allowlisted", true, nil)
			return
		}
	}

	providerName, modelName, parseErr := core.ParseModelString(hello.Model)
	if parseErr != nil {
		h.writeWSError(conn, "session", "bad_request", "invalid model", true, nil)
		return
	}

	requiredHeader, supported := compat.ProviderKeyHeader(providerName)
	if !supported {
		h.writeWSError(conn, "session", "unsupported", "unsupported model provider", true, map[string]any{"provider": providerName})
		return
	}

	llmKey := byokForProvider(hello.BYOK, providerName)
	if strings.TrimSpace(llmKey) == "" {
		h.writeWSError(conn, "session", "unauthorized", fmt.Sprintf("missing provider key for %s", providerName), true, map[string]any{"provider": providerName, "requires_byok_header": requiredHeader})
		return
	}
	cartesiaKey := byokForProvider(hello.BYOK, "cartesia")
	if cartesiaKey == "" {
		h.writeWSError(conn, "session", "unauthorized", "missing cartesia key", true, map[string]any{"requires_byok_header": "X-Provider-Key-Cartesia"})
		return
	}
	if voiceProvider == protocol.VoiceProviderElevenLabs {
		elevenLabsKey := byokForProvider(hello.BYOK, protocol.VoiceProviderElevenLabs)
		if strings.TrimSpace(elevenLabsKey) == "" {
			h.writeWSError(conn, "session", "unauthorized", "missing elevenlabs key", true, map[string]any{"requires_byok_header": "X-Provider-Key-ElevenLabs"})
			return
		}
	}
	serverToolRegistry, liveServerToolErr := h.newLiveServerToolsRegistry(hello)
	if liveServerToolErr != nil {
		h.writeWSError(conn, "session", liveServerToolErr.Code, liveServerToolErr.Message, true, liveServerToolErr.Details)
		return
	}

	provider, err := h.Upstreams.New(providerName, llmKey)
	if err != nil {
		h.writeWSError(conn, "session", "provider_error", "failed to initialize model provider", true, nil)
		return
	}

	voiceHTTPClient := h.HTTPClient
	if voiceHTTPClient == nil {
		voiceHTTPClient = &http.Client{}
	}
	sttProvider := h.newSTTProvider(cartesiaKey, voiceHTTPClient)
	var ttsProvider session.TTSProvider
	if voiceProvider == protocol.VoiceProviderCartesia {
		ttsProvider = h.newTTSProvider(cartesiaKey, voiceHTTPClient)
	}

	sessionID := "s_" + randHex(8)
	ack := protocol.ServerHelloAck{
		Type:            "hello_ack",
		ProtocolVersion: protocol.ProtocolVersion1,
		SessionID:       sessionID,
		AudioIn:         hello.AudioIn,
		AudioOut:        hello.AudioOut,
		Features: protocol.HelloAckFeatures{
			AudioTransport:    transport,
			SupportsAlignment: voiceProvider == protocol.VoiceProviderElevenLabs,
			AlignmentKind: func() string {
				if voiceProvider == protocol.VoiceProviderElevenLabs {
					return protocol.AlignmentKindChar
				}
				return ""
			}(),
		},
		Resume: protocol.HelloAckResume{
			Supported: false,
			Accepted:  false,
			Reason:    "not_implemented",
		},
		Limits: &protocol.HelloAckLimits{
			MaxAudioFrameBytes:  h.Config.LiveMaxAudioFrameBytes,
			MaxJSONMessageBytes: int(h.Config.LiveMaxJSONMessageBytes),
			SilenceCommitMS:     int(h.Config.LiveSilenceCommitDuration / time.Millisecond),
			GraceMS:             int(h.Config.LiveGraceDuration / time.Millisecond),
		},
	}
	if ack.Limits != nil {
		if h.Config.LiveMaxAudioFPS > 0 {
			ack.Limits.MaxAudioFPS = h.Config.LiveMaxAudioFPS
		}
		if h.Config.LiveMaxAudioBytesPerSecond > 0 {
			ack.Limits.MaxAudioBPS = h.Config.LiveMaxAudioBytesPerSecond
		}
		if (h.Config.LiveMaxAudioFPS > 0 || h.Config.LiveMaxAudioBytesPerSecond > 0) && h.Config.LiveInboundBurstSeconds > 0 {
			ack.Limits.InboundBurstSeconds = h.Config.LiveInboundBurstSeconds
		}
	}
	if h.Config.LiveTurnTimeout > 0 && ack.Limits != nil {
		ack.Limits.RunTimeoutMS = int(h.Config.LiveTurnTimeout / time.Millisecond)
	}
	if err := conn.WriteJSON(ack); err != nil {
		return
	}
	startAt := time.Now()
	_ = conn.SetReadDeadline(time.Time{})

	s, err := session.New(session.Dependencies{
		Conn:        conn,
		Logger:      h.Logger,
		Provider:    provider,
		STT:         sttProvider,
		TTS:         ttsProvider,
		ServerTools: serverToolRegistry,
		Hello:       hello,
		SessionID:   sessionID,
		RequestID:   requestIDFromContext(r.Context()),
		ModelName:   modelName,
		StartTime:   startAt,
		Config: session.Config{
			MaxAudioFrameBytes:         h.Config.LiveMaxAudioFrameBytes,
			MaxJSONMessageBytes:        h.Config.LiveMaxJSONMessageBytes,
			LiveMaxAudioFPS:            h.Config.LiveMaxAudioFPS,
			LiveMaxAudioBytesPerSecond: h.Config.LiveMaxAudioBytesPerSecond,
			LiveInboundBurstSeconds:    h.Config.LiveInboundBurstSeconds,
			SilenceCommit:              h.Config.LiveSilenceCommitDuration,
			GracePeriod:                h.Config.LiveGraceDuration,
			PingInterval:               h.Config.LiveWSPingInterval,
			WriteTimeout:               h.Config.LiveWSWriteTimeout,
			ReadTimeout:                h.Config.LiveWSReadTimeout,
			MaxSessionDuration:         h.Config.WSMaxSessionDuration,
			TurnTimeout:                h.Config.LiveTurnTimeout,
			ToolTimeout:                h.Config.LiveToolTimeout,
			MaxToolCallsPerTurn:        h.Config.LiveMaxToolCallsPerTurn,
			MaxModelCallsPerTurn:       h.Config.LiveMaxModelCallsPerTurn,
			MaxUnplayedDuration:        h.Config.LiveMaxUnplayedDuration,
			PlaybackStopWait:           h.Config.LivePlaybackStopWait,
			MaxBackpressurePerMin:      h.Config.LiveMaxBackpressurePerMin,
			ElevenLabsWSBaseURL:        h.Config.LiveElevenLabsWSBaseURL,
			OutboundQueueSize:          128,
			AudioInAckEveryN:           25,
			AudioTransportBinary:       transport == protocol.AudioTransportBinary,
		},
	})
	if err != nil {
		h.writeWSError(conn, "session", "internal", "failed to initialize live session", true, nil)
		return
	}

	unregister := func() {}
	if h.LiveSessions != nil {
		unregister = h.LiveSessions.Register(sessionID, sessions.Handle{
			Cancel: s.Cancel,
			Warn:   s.SendWarning,
		})
	}
	defer unregister()

	if err := s.Run(); err != nil {
		if h.Logger != nil {
			h.Logger.Warn("live session ended with error", "session_id", sessionID, "request_id", requestIDFromContext(r.Context()), "error", err)
		}
	}
}

func (h LiveHandler) originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if len(h.Config.CORSAllowedOrigins) == 0 {
		return false
	}
	_, ok := h.Config.CORSAllowedOrigins[origin]
	return ok
}

func (h LiveHandler) resolveGatewayKey(r *http.Request, hello protocol.ClientHello) string {
	if hello.Auth != nil && strings.TrimSpace(hello.Auth.GatewayAPIKey) != "" {
		return strings.TrimSpace(hello.Auth.GatewayAPIKey)
	}
	return strings.TrimSpace(r.URL.Query().Get("gateway_api_key"))
}

func (h LiveHandler) resolvePrincipal(r *http.Request, apiKey string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	switch h.Config.AuthMode {
	case config.AuthModeRequired:
		if apiKey == "" {
			return "", fmt.Errorf("missing gateway api key")
		}
		if _, ok := h.Config.APIKeys[apiKey]; !ok {
			return "", fmt.Errorf("invalid gateway api key")
		}
		return ratelimit.PrincipalKeyFromAPIKey(apiKey), nil
	case config.AuthModeOptional:
		if apiKey != "" {
			if _, ok := h.Config.APIKeys[apiKey]; !ok {
				return "", fmt.Errorf("invalid gateway api key")
			}
			return ratelimit.PrincipalKeyFromAPIKey(apiKey), nil
		}
		p := principal.Resolve(r, h.Config)
		if strings.TrimSpace(p.Key) == "" {
			return "anonymous", nil
		}
		return p.Key, nil
	case config.AuthModeDisabled:
		p := principal.Resolve(r, h.Config)
		if strings.TrimSpace(p.Key) == "" {
			return "anonymous", nil
		}
		return p.Key, nil
	default:
		return "", fmt.Errorf("invalid auth mode")
	}
}

func (h LiveHandler) newSTTProvider(apiKey string, client *http.Client) session.STTProvider {
	if h.NewSTTProvider != nil {
		return h.NewSTTProvider(apiKey, client)
	}
	return session.STTProviderAdapter{Provider: stt.NewCartesiaWithClient(apiKey, client)}
}

func (h LiveHandler) newTTSProvider(apiKey string, client *http.Client) session.TTSProvider {
	if h.NewTTSProvider != nil {
		return h.NewTTSProvider(apiKey, client)
	}
	return session.TTSProviderAdapter{Provider: tts.NewCartesiaWithClient(apiKey, client)}
}

func byokForProvider(byok protocol.HelloBYOK, provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ""
	}
	if key := strings.TrimSpace(byok.Keys[provider]); key != "" {
		return key
	}

	switch provider {
	case "oai-resp":
		if key := strings.TrimSpace(byok.Keys["oai-resp"]); key != "" {
			return key
		}
		if key := strings.TrimSpace(byok.Keys["openai"]); key != "" {
			return key
		}
		return strings.TrimSpace(byok.OpenAI)
	case "gemini-oauth":
		if key := strings.TrimSpace(byok.Keys["gemini-oauth"]); key != "" {
			return key
		}
		if key := strings.TrimSpace(byok.Keys["gemini"]); key != "" {
			return key
		}
		return strings.TrimSpace(byok.Gemini)
	case "anthropic":
		return strings.TrimSpace(byok.Anthropic)
	case "openai":
		return strings.TrimSpace(byok.OpenAI)
	case "gemini":
		return strings.TrimSpace(byok.Gemini)
	case "groq":
		return strings.TrimSpace(byok.Groq)
	case "cerebras":
		return strings.TrimSpace(byok.Cerebras)
	case "openrouter":
		return strings.TrimSpace(byok.OpenRouter)
	case "cartesia":
		return strings.TrimSpace(byok.Cartesia)
	case "elevenlabs":
		return strings.TrimSpace(byok.ElevenLabs)
	default:
		return strings.TrimSpace(byok.Keys[provider])
	}
}

type liveServerToolError struct {
	Code    string
	Message string
	Details map[string]any
}

func (h LiveHandler) newLiveServerToolsRegistry(hello protocol.ClientHello) (*servertools.Registry, *liveServerToolError) {
	if hello.Tools == nil || len(hello.Tools.ServerTools) == 0 {
		return servertools.NewRegistry(), nil
	}

	enabledSet := make(map[string]struct{}, len(hello.Tools.ServerTools))
	for _, name := range hello.Tools.ServerTools {
		trimmed := strings.TrimSpace(name)
		switch trimmed {
		case servertools.ToolWebSearch, servertools.ToolWebFetch:
		default:
			return nil, &liveServerToolError{
				Code:    "bad_request",
				Message: fmt.Sprintf("unsupported server tool %q", trimmed),
				Details: map[string]any{"error_code": "unsupported_server_tool"},
			}
		}
		enabledSet[trimmed] = struct{}{}
	}

	for name := range hello.Tools.ServerToolConfig {
		if _, ok := enabledSet[name]; !ok {
			return nil, &liveServerToolError{
				Code:    "bad_request",
				Message: fmt.Sprintf("server tool config provided for disabled tool %q", name),
				Details: map[string]any{"error_code": "run_validation_failed"},
			}
		}
	}

	toolHTTPClient := safety.NewRestrictedHTTPClient(h.HTTPClient)
	executors := make([]servertools.Executor, 0, len(hello.Tools.ServerTools))

	if _, ok := enabledSet[servertools.ToolWebSearch]; ok {
		searchCfg, err := servertools.DecodeWebSearchConfig(hello.Tools.ServerToolConfig[servertools.ToolWebSearch])
		if err != nil {
			errorCode := "run_validation_failed"
			if strings.Contains(err.Error(), "unsupported provider") {
				errorCode = "unsupported_tool_provider"
			}
			return nil, &liveServerToolError{
				Code:    "bad_request",
				Message: err.Error(),
				Details: map[string]any{"error_code": errorCode},
			}
		}

		tavilyKey := strings.TrimSpace(byokForProvider(hello.BYOK, servertools.ProviderTavily))
		exaKey := strings.TrimSpace(byokForProvider(hello.BYOK, servertools.ProviderExa))
		if searchCfg.Provider == "" {
			hasTavily := tavilyKey != ""
			hasExa := exaKey != ""
			if hasTavily == hasExa {
				msg := "server_tool_config.vai_web_search.provider is required"
				if hasTavily && hasExa {
					msg = "ambiguous web search provider; set server_tool_config.vai_web_search.provider"
				}
				return nil, &liveServerToolError{
					Code:    "bad_request",
					Message: msg,
					Details: map[string]any{"error_code": "tool_provider_missing"},
				}
			}
			if hasTavily {
				searchCfg.Provider = servertools.ProviderTavily
			} else {
				searchCfg.Provider = servertools.ProviderExa
			}
		}

		var tavilyClient *tavily.Client
		var exaClient *exa.Client
		switch searchCfg.Provider {
		case servertools.ProviderTavily:
			if tavilyKey == "" {
				return nil, &liveServerToolError{
					Code:    "unauthorized",
					Message: "missing tavily key",
					Details: map[string]any{"error_code": "provider_key_missing", "requires_byok_header": servertools.HeaderProviderKeyTavily},
				}
			}
			tavilyClient = tavily.NewClient(tavilyKey, h.Config.TavilyBaseURL, toolHTTPClient)
		case servertools.ProviderExa:
			if exaKey == "" {
				return nil, &liveServerToolError{
					Code:    "unauthorized",
					Message: "missing exa key",
					Details: map[string]any{"error_code": "provider_key_missing", "requires_byok_header": servertools.HeaderProviderKeyExa},
				}
			}
			exaClient = exa.NewClient(exaKey, h.Config.ExaBaseURL, toolHTTPClient)
		default:
			return nil, &liveServerToolError{
				Code:    "bad_request",
				Message: fmt.Sprintf("unsupported web search provider %q", searchCfg.Provider),
				Details: map[string]any{"error_code": "unsupported_tool_provider"},
			}
		}
		executors = append(executors, servertools.NewWebSearchExecutor(searchCfg, tavilyClient, exaClient))
	}

	if _, ok := enabledSet[servertools.ToolWebFetch]; ok {
		fetchCfg, err := servertools.DecodeWebFetchConfig(hello.Tools.ServerToolConfig[servertools.ToolWebFetch])
		if err != nil {
			errorCode := "run_validation_failed"
			if strings.Contains(err.Error(), "unsupported provider") {
				errorCode = "unsupported_tool_provider"
			}
			return nil, &liveServerToolError{
				Code:    "bad_request",
				Message: err.Error(),
				Details: map[string]any{"error_code": errorCode},
			}
		}
		if fetchCfg.Provider == "" {
			fetchCfg.Provider = servertools.ProviderFirecrawl
		}
		firecrawlKey := strings.TrimSpace(byokForProvider(hello.BYOK, servertools.ProviderFirecrawl))
		if firecrawlKey == "" {
			return nil, &liveServerToolError{
				Code:    "unauthorized",
				Message: "missing firecrawl key",
				Details: map[string]any{"error_code": "provider_key_missing", "requires_byok_header": servertools.HeaderProviderKeyFirecrawl},
			}
		}
		firecrawlClient := firecrawl.NewClient(firecrawlKey, h.Config.FirecrawlBaseURL, toolHTTPClient)
		executors = append(executors, servertools.NewWebFetchExecutor(fetchCfg, firecrawlClient))
	}

	return servertools.NewRegistry(executors...), nil
}

func (h LiveHandler) writeWSError(conn *websocket.Conn, scope, code, message string, close bool, details map[string]any) {
	_ = conn.WriteJSON(protocol.ServerError{Type: "error", Scope: scope, Code: code, Message: message, Close: close, Details: details})
	if close {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, message), time.Now().Add(2*time.Second))
	}
}

func randHex(nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func requestIDFromContext(ctx context.Context) string {
	if id, ok := mw.RequestIDFrom(ctx); ok {
		return id
	}
	return ""
}
