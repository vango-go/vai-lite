//go:build integration_proxy
// +build integration_proxy

package integration_proxy_test

import (
	"bufio"
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/server"
	vai "github.com/vango-go/vai-lite/sdk"
)

var (
	testClient *vai.Client
	testCtx    context.Context

	gatewayBaseURL string
)

type proxyProviderConfig struct {
	Name         string
	DefaultModel string
	ModelEnv     string
	RequireKey   func(t *testing.T)
}

func TestMain(m *testing.M) {
	loadEnvFile()
	normalizeEnvKeys()

	cfg := config.Config{
		Addr:                      ":0",
		AuthMode:                  config.AuthModeDisabled,
		APIKeys:                   make(map[string]struct{}),
		TrustProxyHeaders:         false,
		MaxBodyBytes:              8 << 20,
		MaxMessages:               64,
		MaxTools:                  64,
		MaxTotalTextBytes:         512 << 10,
		MaxB64BytesPerBlock:       4 << 20,
		MaxB64BytesTotal:          12 << 20,
		ModelAllowlist:            make(map[string]struct{}),
		CORSAllowedOrigins:        make(map[string]struct{}),
		SSEPingInterval:           15 * time.Second,
		SSEMaxStreamDuration:      5 * time.Minute,
		StreamIdleTimeout:         60 * time.Second,
		WSMaxSessionDuration:      2 * time.Hour,
		WSMaxSessionsPerPrincipal: 4,

		// Live is not used by this suite, but must be non-zero for any handlers that rely on it.
		LiveMaxAudioFrameBytes:     8192,
		LiveMaxJSONMessageBytes:    64 * 1024,
		LiveMaxAudioFPS:            0,
		LiveMaxAudioBytesPerSecond: 0,
		LiveInboundBurstSeconds:    0,
		LiveSilenceCommitDuration:  600 * time.Millisecond,
		LiveGraceDuration:          5 * time.Second,
		LiveWSPingInterval:         20 * time.Second,
		LiveWSWriteTimeout:         5 * time.Second,
		LiveWSReadTimeout:          0,
		LiveHandshakeTimeout:       5 * time.Second,
		LiveTurnTimeout:            30 * time.Second,
		LiveMaxUnplayedDuration:    2500 * time.Millisecond,
		LivePlaybackStopWait:       500 * time.Millisecond,
		LiveToolTimeout:            10 * time.Second,
		LiveMaxToolCallsPerTurn:    5,
		LiveMaxModelCallsPerTurn:   8,
		LiveMaxBackpressurePerMin:  3,
		LiveElevenLabsWSBaseURL:    "",

		LimitRPS:                   50.0,
		LimitBurst:                 100,
		LimitMaxConcurrentRequests: 20,
		LimitMaxConcurrentStreams:  20,

		ReadHeaderTimeout:   10 * time.Second,
		ReadTimeout:         30 * time.Second,
		HandlerTimeout:      2 * time.Minute,
		ShutdownGracePeriod: 30 * time.Second,

		UpstreamConnectTimeout:        5 * time.Second,
		UpstreamResponseHeaderTimeout: 30 * time.Second,

		TavilyBaseURL:    "https://api.tavily.com",
		ExaBaseURL:       "https://api.exa.ai",
		FirecrawlBaseURL: "https://api.firecrawl.dev",
	}

	srv := server.New(cfg, slog.Default())
	ts := httptest.NewServer(srv.Handler())
	gatewayBaseURL = ts.URL

	testClient = vai.NewClient(
		vai.WithBaseURL(gatewayBaseURL),
		vai.WithProviderKey("openai", os.Getenv("OPENAI_API_KEY")),
		vai.WithProviderKey("gemini", os.Getenv("GEMINI_API_KEY")),
		vai.WithProviderKey("openrouter", os.Getenv("OPENROUTER_API_KEY")),
	)
	testCtx = context.Background()

	code := m.Run()

	ts.Close()
	os.Exit(code)
}

func requireOpenAIKey(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
}

func requireGeminiKey(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
}

func requireOpenRouterKey(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
}

func selectedProxyProviders(t *testing.T) []proxyProviderConfig {
	t.Helper()

	all := []proxyProviderConfig{
		{
			Name:         "oai-resp",
			DefaultModel: "oai-resp/gpt-5-mini",
			ModelEnv:     "VAI_INTEGRATION_OAI_RESP_MODEL",
			RequireKey:   requireOpenAIKey,
		},
		{
			Name:         "gemini",
			DefaultModel: "gemini/gemini-3-flash-preview",
			ModelEnv:     "VAI_INTEGRATION_GEMINI_MODEL",
			RequireKey:   requireGeminiKey,
		},
		{
			Name:         "openrouter",
			DefaultModel: "openrouter/z-ai/glm-4.7",
			ModelEnv:     "VAI_INTEGRATION_OPENROUTER_MODEL",
			RequireKey:   requireOpenRouterKey,
		},
	}

	filter := strings.ToLower(strings.TrimSpace(getenvDefault("VAI_INTEGRATION_PROXY_PROVIDER", "all")))
	switch filter {
	case "", "all":
		return all
	case "oai-resp", "oairesp", "oai", "openai":
		return all[:1]
	case "gemini":
		return []proxyProviderConfig{all[1]}
	case "openrouter":
		return []proxyProviderConfig{all[2]}
	default:
		t.Fatalf("unsupported VAI_INTEGRATION_PROXY_PROVIDER=%q (expected all|oai-resp|gemini|openrouter)", filter)
		return nil
	}
}

func forEachProxyProvider(t *testing.T, fn func(t *testing.T, provider proxyProviderConfig)) {
	t.Helper()
	for _, provider := range selectedProxyProviders(t) {
		provider := provider
		t.Run(provider.Name, func(t *testing.T) {
			provider.RequireKey(t)
			fn(t, provider)
		})
	}
}

func providerModel(provider proxyProviderConfig) string {
	return strings.TrimSpace(getenvDefault(provider.ModelEnv, provider.DefaultModel))
}

func testContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func defaultTestContext(t *testing.T) context.Context {
	t.Helper()
	return testContext(t, 60*time.Second)
}

func normalizeEnvKeys() {
	if os.Getenv("GEMINI_API_KEY") == "" {
		if googleKey := os.Getenv("GOOGLE_API_KEY"); googleKey != "" {
			os.Setenv("GEMINI_API_KEY", googleKey)
		}
	}
}

func loadEnvFile() {
	_, filename, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(filename), "..")
	envPath := filepath.Join(projectRoot, ".env")

	file, err := os.Open(envPath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
}

func getenvDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
