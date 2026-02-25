package config

import (
	"strings"
	"testing"
	"time"
)

var gatewayEnvKeys = []string{
	"VAI_PROXY_ADDR",
	"VAI_PROXY_AUTH_MODE",
	"VAI_PROXY_API_KEYS",
	"VAI_PROXY_TRUST_PROXY_HEADERS",
	"VAI_PROXY_CORS_ORIGINS",
	"VAI_PROXY_MAX_BODY_BYTES",
	"VAI_PROXY_MAX_MESSAGES",
	"VAI_PROXY_MAX_TOTAL_TEXT_BYTES",
	"VAI_PROXY_MAX_TOOLS",
	"VAI_PROXY_MAX_B64_PER_BLOCK",
	"VAI_PROXY_MAX_B64_TOTAL",
	"VAI_PROXY_SSE_PING_INTERVAL",
	"VAI_PROXY_SSE_MAX_DURATION",
	"VAI_PROXY_STREAM_IDLE_TIMEOUT",
	"VAI_PROXY_MAX_STREAMS_PER_PRINCIPAL",
	"VAI_PROXY_WS_MAX_DURATION",
	"VAI_PROXY_WS_MAX_SESSIONS_PER_PRINCIPAL",
	"VAI_PROXY_CONNECT_TIMEOUT",
	"VAI_PROXY_RESPONSE_HEADER_TIMEOUT",
	"VAI_PROXY_TAVILY_API_KEY",
	"VAI_PROXY_TAVILY_BASE_URL",
	"VAI_PROXY_FIRECRAWL_API_KEY",
	"VAI_PROXY_FIRECRAWL_BASE_URL",
	"VAI_PROXY_TOTAL_REQUEST_TIMEOUT",
	"VAI_PROXY_RATE_LIMIT_RPS",
	"VAI_PROXY_RATE_LIMIT_BURST",
	"VAI_PROXY_MODEL_ALLOWLIST",
	"VAI_PROXY_MAX_CONCURRENT_REQUESTS",
	"VAI_PROXY_READ_HEADER_TIMEOUT",
	"VAI_PROXY_READ_TIMEOUT",
	"VAI_PROXY_SHUTDOWN_GRACE_PERIOD",
	"VAI_GATEWAY_ADDR",
	"VAI_AUTH_MODE",
	"VAI_API_KEYS",
	"VAI_CORS_ALLOWED_ORIGINS",
	"VAI_MAX_BODY_BYTES",
	"VAI_MAX_MESSAGES",
	"VAI_MAX_TOTAL_TEXT_BYTES",
	"VAI_MAX_TOOLS",
	"VAI_MAX_B64_BYTES_PER_BLOCK",
	"VAI_MAX_B64_BYTES_TOTAL",
	"VAI_SSE_PING_INTERVAL",
	"VAI_SSE_MAX_STREAM_DURATION",
	"VAI_LIMIT_RPS",
	"VAI_LIMIT_BURST",
	"VAI_LIMIT_MAX_CONCURRENT_REQUESTS",
	"VAI_LIMIT_MAX_CONCURRENT_STREAMS",
	"VAI_READ_HEADER_TIMEOUT",
	"VAI_READ_TIMEOUT",
	"VAI_HANDLER_TIMEOUT",
	"VAI_UPSTREAM_RESPONSE_HEADER_TIMEOUT",
	"VAI_MODEL_ALLOWLIST",
}

func clearGatewayEnv(t *testing.T) {
	t.Helper()
	for _, key := range gatewayEnvKeys {
		t.Setenv(key, "")
	}
}

func TestLoadFromEnv_DefaultsMatchSpec(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("VAI_PROXY_API_KEYS", "vai_sk_test")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.AuthMode != AuthModeRequired {
		t.Fatalf("AuthMode = %q, want %q", cfg.AuthMode, AuthModeRequired)
	}
	if cfg.MaxBodyBytes != 8<<20 {
		t.Fatalf("MaxBodyBytes = %d, want %d", cfg.MaxBodyBytes, int64(8<<20))
	}
	if cfg.TrustProxyHeaders != false {
		t.Fatalf("TrustProxyHeaders = %v, want false", cfg.TrustProxyHeaders)
	}
	if cfg.MaxMessages != 64 {
		t.Fatalf("MaxMessages = %d, want 64", cfg.MaxMessages)
	}
	if cfg.MaxTotalTextBytes != 512<<10 {
		t.Fatalf("MaxTotalTextBytes = %d, want %d", cfg.MaxTotalTextBytes, int64(512<<10))
	}
	if cfg.MaxTools != 64 {
		t.Fatalf("MaxTools = %d, want 64", cfg.MaxTools)
	}
	if cfg.MaxB64BytesPerBlock != 4<<20 {
		t.Fatalf("MaxB64BytesPerBlock = %d, want %d", cfg.MaxB64BytesPerBlock, int64(4<<20))
	}
	if cfg.MaxB64BytesTotal != 12<<20 {
		t.Fatalf("MaxB64BytesTotal = %d, want %d", cfg.MaxB64BytesTotal, int64(12<<20))
	}
	if cfg.SSEPingInterval != 15*time.Second {
		t.Fatalf("SSEPingInterval = %v, want 15s", cfg.SSEPingInterval)
	}
	if cfg.SSEMaxStreamDuration != 5*time.Minute {
		t.Fatalf("SSEMaxStreamDuration = %v, want 5m", cfg.SSEMaxStreamDuration)
	}
	if cfg.StreamIdleTimeout != 60*time.Second {
		t.Fatalf("StreamIdleTimeout = %v, want 60s", cfg.StreamIdleTimeout)
	}
	if cfg.LimitMaxConcurrentStreams != 4 {
		t.Fatalf("LimitMaxConcurrentStreams = %d, want 4", cfg.LimitMaxConcurrentStreams)
	}
	if cfg.WSMaxSessionDuration != 2*time.Hour {
		t.Fatalf("WSMaxSessionDuration = %v, want 2h", cfg.WSMaxSessionDuration)
	}
	if cfg.WSMaxSessionsPerPrincipal != 2 {
		t.Fatalf("WSMaxSessionsPerPrincipal = %d, want 2", cfg.WSMaxSessionsPerPrincipal)
	}
	if cfg.UpstreamConnectTimeout != 5*time.Second {
		t.Fatalf("UpstreamConnectTimeout = %v, want 5s", cfg.UpstreamConnectTimeout)
	}
	if cfg.UpstreamResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("UpstreamResponseHeaderTimeout = %v, want 30s", cfg.UpstreamResponseHeaderTimeout)
	}
	if cfg.TavilyAPIKey != "" {
		t.Fatalf("TavilyAPIKey = %q, want empty", cfg.TavilyAPIKey)
	}
	if cfg.TavilyBaseURL != "https://api.tavily.com" {
		t.Fatalf("TavilyBaseURL = %q", cfg.TavilyBaseURL)
	}
	if cfg.FirecrawlAPIKey != "" {
		t.Fatalf("FirecrawlAPIKey = %q, want empty", cfg.FirecrawlAPIKey)
	}
	if cfg.FirecrawlBaseURL != "https://api.firecrawl.dev" {
		t.Fatalf("FirecrawlBaseURL = %q", cfg.FirecrawlBaseURL)
	}
	if cfg.HandlerTimeout != 2*time.Minute {
		t.Fatalf("HandlerTimeout = %v, want 2m", cfg.HandlerTimeout)
	}
	if cfg.ShutdownGracePeriod != 30*time.Second {
		t.Fatalf("ShutdownGracePeriod = %v, want 30s", cfg.ShutdownGracePeriod)
	}
}

func TestLoadFromEnv_UsesProxyEnvOverrides(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("VAI_PROXY_ADDR", ":9090")
	t.Setenv("VAI_PROXY_AUTH_MODE", "optional")
	t.Setenv("VAI_PROXY_API_KEYS", "k1,k2")
	t.Setenv("VAI_PROXY_TRUST_PROXY_HEADERS", "true")
	t.Setenv("VAI_PROXY_CORS_ORIGINS", "https://a.example,https://b.example")
	t.Setenv("VAI_PROXY_MAX_BODY_BYTES", "12345")
	t.Setenv("VAI_PROXY_MAX_MESSAGES", "11")
	t.Setenv("VAI_PROXY_MAX_TOTAL_TEXT_BYTES", "6789")
	t.Setenv("VAI_PROXY_MAX_TOOLS", "7")
	t.Setenv("VAI_PROXY_MAX_B64_PER_BLOCK", "300")
	t.Setenv("VAI_PROXY_MAX_B64_TOTAL", "900")
	t.Setenv("VAI_PROXY_SSE_PING_INTERVAL", "17s")
	t.Setenv("VAI_PROXY_SSE_MAX_DURATION", "4m")
	t.Setenv("VAI_PROXY_STREAM_IDLE_TIMEOUT", "31s")
	t.Setenv("VAI_PROXY_WS_MAX_DURATION", "95m")
	t.Setenv("VAI_PROXY_WS_MAX_SESSIONS_PER_PRINCIPAL", "5")
	t.Setenv("VAI_PROXY_RATE_LIMIT_RPS", "3.5")
	t.Setenv("VAI_PROXY_RATE_LIMIT_BURST", "8")
	t.Setenv("VAI_PROXY_MAX_CONCURRENT_REQUESTS", "44")
	t.Setenv("VAI_PROXY_MAX_STREAMS_PER_PRINCIPAL", "6")
	t.Setenv("VAI_PROXY_READ_HEADER_TIMEOUT", "12s")
	t.Setenv("VAI_PROXY_READ_TIMEOUT", "33s")
	t.Setenv("VAI_PROXY_TOTAL_REQUEST_TIMEOUT", "90s")
	t.Setenv("VAI_PROXY_SHUTDOWN_GRACE_PERIOD", "31s")
	t.Setenv("VAI_PROXY_CONNECT_TIMEOUT", "7s")
	t.Setenv("VAI_PROXY_RESPONSE_HEADER_TIMEOUT", "29s")
	t.Setenv("VAI_PROXY_TAVILY_API_KEY", "tvly")
	t.Setenv("VAI_PROXY_TAVILY_BASE_URL", "https://t.example")
	t.Setenv("VAI_PROXY_FIRECRAWL_API_KEY", "fcr")
	t.Setenv("VAI_PROXY_FIRECRAWL_BASE_URL", "https://f.example")
	t.Setenv("VAI_PROXY_MODEL_ALLOWLIST", "anthropic/a,openai/b")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.Addr != ":9090" || cfg.AuthMode != AuthModeOptional {
		t.Fatalf("Addr/AuthMode = %q/%q", cfg.Addr, cfg.AuthMode)
	}
	if cfg.MaxBodyBytes != 12345 || cfg.MaxMessages != 11 || cfg.MaxTotalTextBytes != 6789 || cfg.MaxTools != 7 {
		t.Fatalf("message limits mismatch: %+v", cfg)
	}
	if cfg.MaxB64BytesPerBlock != 300 || cfg.MaxB64BytesTotal != 900 {
		t.Fatalf("base64 limits mismatch: %d/%d", cfg.MaxB64BytesPerBlock, cfg.MaxB64BytesTotal)
	}
	if cfg.SSEPingInterval != 17*time.Second || cfg.SSEMaxStreamDuration != 4*time.Minute || cfg.StreamIdleTimeout != 31*time.Second {
		t.Fatalf("stream durations mismatch: %v/%v/%v", cfg.SSEPingInterval, cfg.SSEMaxStreamDuration, cfg.StreamIdleTimeout)
	}
	if cfg.WSMaxSessionDuration != 95*time.Minute || cfg.WSMaxSessionsPerPrincipal != 5 {
		t.Fatalf("ws limits mismatch: %v/%d", cfg.WSMaxSessionDuration, cfg.WSMaxSessionsPerPrincipal)
	}
	if cfg.LimitRPS != 3.5 || cfg.LimitBurst != 8 || cfg.LimitMaxConcurrentRequests != 44 || cfg.LimitMaxConcurrentStreams != 6 {
		t.Fatalf("rate/concurrency mismatch: %v/%d/%d/%d", cfg.LimitRPS, cfg.LimitBurst, cfg.LimitMaxConcurrentRequests, cfg.LimitMaxConcurrentStreams)
	}
	if cfg.ReadHeaderTimeout != 12*time.Second || cfg.ReadTimeout != 33*time.Second || cfg.HandlerTimeout != 90*time.Second {
		t.Fatalf("server timeouts mismatch: %v/%v/%v", cfg.ReadHeaderTimeout, cfg.ReadTimeout, cfg.HandlerTimeout)
	}
	if cfg.ShutdownGracePeriod != 31*time.Second {
		t.Fatalf("ShutdownGracePeriod = %v, want 31s", cfg.ShutdownGracePeriod)
	}
	if cfg.UpstreamConnectTimeout != 7*time.Second || cfg.UpstreamResponseHeaderTimeout != 29*time.Second {
		t.Fatalf("upstream timeouts mismatch: %v/%v", cfg.UpstreamConnectTimeout, cfg.UpstreamResponseHeaderTimeout)
	}
	if cfg.TavilyAPIKey != "tvly" || cfg.TavilyBaseURL != "https://t.example" {
		t.Fatalf("tavily mismatch: %q/%q", cfg.TavilyAPIKey, cfg.TavilyBaseURL)
	}
	if cfg.FirecrawlAPIKey != "fcr" || cfg.FirecrawlBaseURL != "https://f.example" {
		t.Fatalf("firecrawl mismatch: %q/%q", cfg.FirecrawlAPIKey, cfg.FirecrawlBaseURL)
	}
	if len(cfg.APIKeys) != 2 {
		t.Fatalf("APIKeys len=%d, want 2", len(cfg.APIKeys))
	}
	if _, ok := cfg.APIKeys["k1"]; !ok {
		t.Fatalf("expected API key k1")
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf("CORSAllowedOrigins len=%d, want 2", len(cfg.CORSAllowedOrigins))
	}
	if len(cfg.ModelAllowlist) != 2 {
		t.Fatalf("ModelAllowlist len=%d, want 2", len(cfg.ModelAllowlist))
	}
	if !cfg.TrustProxyHeaders {
		t.Fatalf("TrustProxyHeaders = false, want true")
	}
}

func TestLoadFromEnv_IgnoresLegacyEnvNames(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("VAI_PROXY_AUTH_MODE", "optional")
	t.Setenv("VAI_GATEWAY_ADDR", ":9999")
	t.Setenv("VAI_MAX_BODY_BYTES", "999999")
	t.Setenv("VAI_SSE_MAX_STREAM_DURATION", "42m")
	t.Setenv("VAI_LIMIT_MAX_CONCURRENT_STREAMS", "123")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, expected proxy default :8080", cfg.Addr)
	}
	if cfg.MaxBodyBytes != 8<<20 {
		t.Fatalf("MaxBodyBytes = %d, expected proxy default %d", cfg.MaxBodyBytes, int64(8<<20))
	}
	if cfg.SSEMaxStreamDuration != 5*time.Minute {
		t.Fatalf("SSEMaxStreamDuration = %v, expected proxy default 5m", cfg.SSEMaxStreamDuration)
	}
	if cfg.LimitMaxConcurrentStreams != 4 {
		t.Fatalf("LimitMaxConcurrentStreams = %d, expected proxy default 4", cfg.LimitMaxConcurrentStreams)
	}
}

func TestLoadFromEnv_RequiredAuthNeedsAPIKeys(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("VAI_PROXY_AUTH_MODE", "required")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "VAI_PROXY_API_KEYS") {
		t.Fatalf("error = %v, expected VAI_PROXY_API_KEYS in message", err)
	}
}

func TestLoadFromEnv_ParsesCSVAllowlists(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("VAI_PROXY_AUTH_MODE", "optional")
	t.Setenv("VAI_PROXY_MODEL_ALLOWLIST", "anthropic/a, openai/b,,")
	t.Setenv("VAI_PROXY_CORS_ORIGINS", "https://one.example, https://two.example,,")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if len(cfg.ModelAllowlist) != 2 {
		t.Fatalf("ModelAllowlist len=%d, want 2", len(cfg.ModelAllowlist))
	}
	if _, ok := cfg.ModelAllowlist["anthropic/a"]; !ok {
		t.Fatalf("missing anthropic/a")
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf("CORSAllowedOrigins len=%d, want 2", len(cfg.CORSAllowedOrigins))
	}
	if _, ok := cfg.CORSAllowedOrigins["https://two.example"]; !ok {
		t.Fatalf("missing https://two.example")
	}
}

func TestLoadFromEnv_InvalidDurationsAndBounds(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		errSubstr string
	}{
		{
			name: "invalid sse max duration",
			env: map[string]string{
				"VAI_PROXY_AUTH_MODE":        "optional",
				"VAI_PROXY_SSE_MAX_DURATION": "0s",
			},
			errSubstr: "VAI_PROXY_SSE_MAX_DURATION",
		},
		{
			name: "invalid connect timeout",
			env: map[string]string{
				"VAI_PROXY_AUTH_MODE":       "optional",
				"VAI_PROXY_CONNECT_TIMEOUT": "0s",
			},
			errSubstr: "VAI_PROXY_CONNECT_TIMEOUT",
		},
		{
			name: "invalid shutdown grace period",
			env: map[string]string{
				"VAI_PROXY_AUTH_MODE":             "optional",
				"VAI_PROXY_SHUTDOWN_GRACE_PERIOD": "0s",
			},
			errSubstr: "VAI_PROXY_SHUTDOWN_GRACE_PERIOD",
		},
		{
			name: "invalid ws sessions",
			env: map[string]string{
				"VAI_PROXY_AUTH_MODE":                     "optional",
				"VAI_PROXY_WS_MAX_SESSIONS_PER_PRINCIPAL": "0",
			},
			errSubstr: "VAI_PROXY_WS_MAX_SESSIONS_PER_PRINCIPAL",
		},
		{
			name: "invalid b64 budgets",
			env: map[string]string{
				"VAI_PROXY_AUTH_MODE":         "optional",
				"VAI_PROXY_MAX_B64_PER_BLOCK": "100",
				"VAI_PROXY_MAX_B64_TOTAL":     "99",
			},
			errSubstr: "VAI_PROXY_MAX_B64_PER_BLOCK must be <=",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearGatewayEnv(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			_, err := LoadFromEnv()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("error = %v, expected substring %q", err, tc.errSubstr)
			}
		})
	}
}
