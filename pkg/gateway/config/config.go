package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type AuthMode string

const (
	AuthModeRequired AuthMode = "required"
	AuthModeOptional AuthMode = "optional"
	AuthModeDisabled AuthMode = "disabled"
)

type Config struct {
	Addr string

	AuthMode AuthMode
	APIKeys  map[string]struct{}

	// If true, client identity may be derived from proxy headers like X-Forwarded-For.
	// This should only be enabled when the gateway is deployed behind a trusted proxy/LB.
	TrustProxyHeaders bool

	MaxBodyBytes int64

	// Request-shape limits (enforced after strict decode).
	MaxMessages       int
	MaxTools          int
	MaxTotalTextBytes int64

	// Base64 budgets (decoded bytes) enforced before expensive decode work.
	MaxB64BytesPerBlock int64
	MaxB64BytesTotal    int64

	// Optional allowlist of canonical model IDs (provider/model).
	ModelAllowlist map[string]struct{}

	// CORS
	CORSAllowedOrigins map[string]struct{} // empty => disabled

	// SSE
	SSEPingInterval           time.Duration
	SSEMaxStreamDuration      time.Duration
	StreamIdleTimeout         time.Duration
	WSMaxSessionDuration      time.Duration
	WSMaxSessionsPerPrincipal int

	// Live WebSocket mode (/v1/live).
	LiveMaxAudioFrameBytes     int
	LiveMaxJSONMessageBytes    int64
	LiveMaxAudioFPS            int
	LiveMaxAudioBytesPerSecond int64
	LiveInboundBurstSeconds    int
	LiveSilenceCommitDuration  time.Duration
	LiveGraceDuration          time.Duration
	LiveWSPingInterval         time.Duration
	LiveWSWriteTimeout         time.Duration
	LiveWSReadTimeout          time.Duration
	LiveHandshakeTimeout       time.Duration
	LiveTurnTimeout            time.Duration

	// In-memory limits (per principal).
	LimitRPS                   float64
	LimitBurst                 int
	LimitMaxConcurrentRequests int
	LimitMaxConcurrentStreams  int

	// Operational defaults
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	HandlerTimeout      time.Duration
	ShutdownGracePeriod time.Duration

	// Upstream HTTP client defaults
	UpstreamConnectTimeout        time.Duration
	UpstreamResponseHeaderTimeout time.Duration

	// Gateway-managed server tool backends.
	TavilyBaseURL    string
	ExaBaseURL       string
	FirecrawlBaseURL string
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		Addr:                          envOr("VAI_PROXY_ADDR", ":8080"),
		AuthMode:                      AuthMode(envOr("VAI_PROXY_AUTH_MODE", string(AuthModeRequired))),
		APIKeys:                       make(map[string]struct{}),
		TrustProxyHeaders:             envBoolOr("VAI_PROXY_TRUST_PROXY_HEADERS", false),
		MaxBodyBytes:                  envInt64Or("VAI_PROXY_MAX_BODY_BYTES", 8<<20), // 8 MiB
		MaxMessages:                   envIntOr("VAI_PROXY_MAX_MESSAGES", 64),
		MaxTools:                      envIntOr("VAI_PROXY_MAX_TOOLS", 64),
		MaxTotalTextBytes:             envInt64Or("VAI_PROXY_MAX_TOTAL_TEXT_BYTES", 512<<10), // 512 KiB
		MaxB64BytesPerBlock:           envInt64Or("VAI_PROXY_MAX_B64_PER_BLOCK", 4<<20),      // 4 MiB decoded
		MaxB64BytesTotal:              envInt64Or("VAI_PROXY_MAX_B64_TOTAL", 12<<20),         // 12 MiB decoded
		ModelAllowlist:                make(map[string]struct{}),
		CORSAllowedOrigins:            make(map[string]struct{}),
		SSEPingInterval:               envDurationOr("VAI_PROXY_SSE_PING_INTERVAL", 15*time.Second),
		SSEMaxStreamDuration:          envDurationOr("VAI_PROXY_SSE_MAX_DURATION", 5*time.Minute),
		StreamIdleTimeout:             envDurationOr("VAI_PROXY_STREAM_IDLE_TIMEOUT", 60*time.Second),
		WSMaxSessionDuration:          envDurationOr("VAI_PROXY_WS_MAX_DURATION", 2*time.Hour),
		WSMaxSessionsPerPrincipal:     envIntOr("VAI_PROXY_WS_MAX_SESSIONS_PER_PRINCIPAL", 2),
		LiveMaxAudioFrameBytes:        envIntOr("VAI_PROXY_LIVE_MAX_AUDIO_FRAME_BYTES", 8192),
		LiveMaxJSONMessageBytes:       envInt64Or("VAI_PROXY_LIVE_MAX_JSON_MESSAGE_BYTES", 64*1024),
		LiveMaxAudioFPS:               envIntOr("VAI_PROXY_LIVE_MAX_AUDIO_FPS", 120),
		LiveMaxAudioBytesPerSecond:    envInt64Or("VAI_PROXY_LIVE_MAX_AUDIO_BPS", 128*1024),
		LiveInboundBurstSeconds:       envIntOr("VAI_PROXY_LIVE_INBOUND_BURST_SECONDS", 2),
		LiveSilenceCommitDuration:     envDurationOr("VAI_PROXY_LIVE_SILENCE_COMMIT_MS", 600*time.Millisecond),
		LiveGraceDuration:             envDurationOr("VAI_PROXY_LIVE_GRACE_MS", 5*time.Second),
		LiveWSPingInterval:            envDurationOr("VAI_PROXY_LIVE_WS_PING_INTERVAL", 20*time.Second),
		LiveWSWriteTimeout:            envDurationOr("VAI_PROXY_LIVE_WS_WRITE_TIMEOUT", 5*time.Second),
		LiveWSReadTimeout:             envDurationOr("VAI_PROXY_LIVE_WS_READ_TIMEOUT", 0),
		LiveHandshakeTimeout:          envDurationOr("VAI_PROXY_LIVE_HANDSHAKE_TIMEOUT", 5*time.Second),
		LiveTurnTimeout:               envDurationOr("VAI_PROXY_LIVE_TURN_TIMEOUT", 30*time.Second),
		LimitRPS:                      envFloat64Or("VAI_PROXY_RATE_LIMIT_RPS", 2.0),
		LimitBurst:                    envIntOr("VAI_PROXY_RATE_LIMIT_BURST", 4),
		LimitMaxConcurrentRequests:    envIntOr("VAI_PROXY_MAX_CONCURRENT_REQUESTS", 20),
		LimitMaxConcurrentStreams:     envIntOr("VAI_PROXY_MAX_STREAMS_PER_PRINCIPAL", 4),
		ReadHeaderTimeout:             envDurationOr("VAI_PROXY_READ_HEADER_TIMEOUT", 10*time.Second),
		ReadTimeout:                   envDurationOr("VAI_PROXY_READ_TIMEOUT", 30*time.Second),
		HandlerTimeout:                envDurationOr("VAI_PROXY_TOTAL_REQUEST_TIMEOUT", 2*time.Minute),
		ShutdownGracePeriod:           envDurationOr("VAI_PROXY_SHUTDOWN_GRACE_PERIOD", 30*time.Second),
		UpstreamConnectTimeout:        envDurationOr("VAI_PROXY_CONNECT_TIMEOUT", 5*time.Second),
		UpstreamResponseHeaderTimeout: envDurationOr("VAI_PROXY_RESPONSE_HEADER_TIMEOUT", 30*time.Second),
		TavilyBaseURL:                 envOr("VAI_PROXY_TAVILY_BASE_URL", "https://api.tavily.com"),
		ExaBaseURL:                    envOr("VAI_PROXY_EXA_BASE_URL", "https://api.exa.ai"),
		FirecrawlBaseURL:              envOr("VAI_PROXY_FIRECRAWL_BASE_URL", "https://api.firecrawl.dev"),
	}

	switch cfg.AuthMode {
	case AuthModeRequired, AuthModeOptional, AuthModeDisabled:
	default:
		return Config{}, fmt.Errorf("VAI_PROXY_AUTH_MODE must be one of required|optional|disabled")
	}

	for _, key := range splitCSV(os.Getenv("VAI_PROXY_API_KEYS")) {
		cfg.APIKeys[key] = struct{}{}
	}

	for _, m := range splitCSV(os.Getenv("VAI_PROXY_MODEL_ALLOWLIST")) {
		cfg.ModelAllowlist[m] = struct{}{}
	}

	for _, origin := range splitCSV(os.Getenv("VAI_PROXY_CORS_ORIGINS")) {
		cfg.CORSAllowedOrigins[origin] = struct{}{}
	}

	if cfg.MaxBodyBytes <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_BODY_BYTES must be > 0")
	}

	if cfg.MaxMessages <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_MESSAGES must be > 0")
	}
	if cfg.MaxTools <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_TOOLS must be > 0")
	}
	if cfg.MaxTotalTextBytes <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_TOTAL_TEXT_BYTES must be > 0")
	}
	if cfg.MaxB64BytesPerBlock <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_B64_PER_BLOCK must be > 0")
	}
	if cfg.MaxB64BytesTotal <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_B64_TOTAL must be > 0")
	}
	if cfg.MaxB64BytesPerBlock > cfg.MaxB64BytesTotal {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_B64_PER_BLOCK must be <= VAI_PROXY_MAX_B64_TOTAL")
	}
	if cfg.SSEPingInterval <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_SSE_PING_INTERVAL must be > 0")
	}
	if cfg.SSEMaxStreamDuration <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_SSE_MAX_DURATION must be > 0")
	}
	if cfg.StreamIdleTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_STREAM_IDLE_TIMEOUT must be > 0")
	}
	if cfg.WSMaxSessionDuration <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_WS_MAX_DURATION must be > 0")
	}
	if cfg.WSMaxSessionsPerPrincipal <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_WS_MAX_SESSIONS_PER_PRINCIPAL must be > 0")
	}
	if cfg.LiveMaxAudioFrameBytes <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_MAX_AUDIO_FRAME_BYTES must be > 0")
	}
	if cfg.LiveMaxJSONMessageBytes <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_MAX_JSON_MESSAGE_BYTES must be > 0")
	}
	if cfg.LiveMaxAudioFPS < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_MAX_AUDIO_FPS must be >= 0")
	}
	if cfg.LiveMaxAudioBytesPerSecond < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_MAX_AUDIO_BPS must be >= 0")
	}
	if cfg.LiveInboundBurstSeconds < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_INBOUND_BURST_SECONDS must be >= 0")
	}
	if (cfg.LiveMaxAudioFPS > 0 || cfg.LiveMaxAudioBytesPerSecond > 0) && cfg.LiveInboundBurstSeconds < 1 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_INBOUND_BURST_SECONDS must be >= 1 when inbound audio limits are enabled")
	}
	if cfg.LiveSilenceCommitDuration <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_SILENCE_COMMIT_MS must be > 0")
	}
	if cfg.LiveGraceDuration <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_GRACE_MS must be > 0")
	}
	if cfg.LiveWSPingInterval <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_WS_PING_INTERVAL must be > 0")
	}
	if cfg.LiveWSWriteTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_WS_WRITE_TIMEOUT must be > 0")
	}
	if cfg.LiveWSReadTimeout < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_WS_READ_TIMEOUT must be >= 0")
	}
	if cfg.LiveHandshakeTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_HANDSHAKE_TIMEOUT must be > 0")
	}
	if cfg.LiveTurnTimeout < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_LIVE_TURN_TIMEOUT must be >= 0")
	}
	if cfg.ReadHeaderTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_READ_HEADER_TIMEOUT must be > 0")
	}
	if cfg.ReadTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_READ_TIMEOUT must be > 0")
	}
	if cfg.HandlerTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_TOTAL_REQUEST_TIMEOUT must be > 0")
	}
	if cfg.ShutdownGracePeriod <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_SHUTDOWN_GRACE_PERIOD must be > 0")
	}
	if cfg.UpstreamConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_CONNECT_TIMEOUT must be > 0")
	}
	if cfg.UpstreamResponseHeaderTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_RESPONSE_HEADER_TIMEOUT must be > 0")
	}
	if strings.TrimSpace(cfg.TavilyBaseURL) == "" {
		return Config{}, fmt.Errorf("VAI_PROXY_TAVILY_BASE_URL must not be empty")
	}
	if strings.TrimSpace(cfg.ExaBaseURL) == "" {
		return Config{}, fmt.Errorf("VAI_PROXY_EXA_BASE_URL must not be empty")
	}
	if strings.TrimSpace(cfg.FirecrawlBaseURL) == "" {
		return Config{}, fmt.Errorf("VAI_PROXY_FIRECRAWL_BASE_URL must not be empty")
	}

	if cfg.LimitRPS < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_RATE_LIMIT_RPS must be >= 0")
	}
	if cfg.LimitBurst < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_RATE_LIMIT_BURST must be >= 0")
	}
	if cfg.LimitMaxConcurrentRequests < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_CONCURRENT_REQUESTS must be >= 0")
	}
	if cfg.LimitMaxConcurrentStreams < 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_MAX_STREAMS_PER_PRINCIPAL must be >= 0")
	}

	if cfg.AuthMode == AuthModeRequired && len(cfg.APIKeys) == 0 {
		return Config{}, fmt.Errorf("VAI_PROXY_API_KEYS must be set when VAI_PROXY_AUTH_MODE=required")
	}

	return cfg, nil
}

func envOr(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func envInt64Or(key string, def int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func envIntOr(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func envFloat64Or(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def
	}
	return n
}

func envBoolOr(key string, def bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}

func envDurationOr(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return def
	}
	return d
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
