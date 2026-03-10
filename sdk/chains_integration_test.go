package vai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/internal/db"
	"github.com/vango-go/vai-lite/internal/dotenv"
	chainrt "github.com/vango-go/vai-lite/pkg/gateway/chains"
	gatewayconfig "github.com/vango-go/vai-lite/pkg/gateway/config"
	gatewayserver "github.com/vango-go/vai-lite/pkg/gateway/server"
)

func TestChainsConnect_Run_WebsocketRealProvider(t *testing.T) {
	if os.Getenv("VAI_SMOKE_REAL") != "1" {
		t.Skip("set VAI_SMOKE_REAL=1 to run real chain websocket smoke tests")
	}

	repoRoot := switchToRepoRoot(t)
	_ = repoRoot
	loadEnvForSmoke(t)

	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if openAIKey == "" {
		t.Skip("OPENAI_API_KEY is required for this smoke test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var opts gatewayserver.Options
	if pooled := strings.TrimSpace(os.Getenv("DATABASE_URL")); pooled != "" {
		direct := strings.TrimSpace(os.Getenv("DATABASE_URL_DIRECT"))
		if direct == "" {
			t.Skip("DATABASE_URL_DIRECT is required when DATABASE_URL is set")
		}
		if err := db.MigrateUp(ctx, direct); err != nil {
			t.Fatalf("MigrateUp() error = %v", err)
		}
		pool, err := db.ConnectPool(ctx)
		if err != nil {
			t.Fatalf("ConnectPool() error = %v", err)
		}
		t.Cleanup(pool.Close)
		opts.ChainStore = chainrt.NewPostgresStore(pool)
	}

	cfg := gatewayconfig.Config{
		AuthMode:                      gatewayconfig.AuthModeDisabled,
		APIKeys:                       map[string]struct{}{},
		CORSAllowedOrigins:            map[string]struct{}{},
		ModelAllowlist:                map[string]struct{}{},
		SSEPingInterval:               15 * time.Second,
		SSEMaxStreamDuration:          2 * time.Minute,
		StreamIdleTimeout:             60 * time.Second,
		WSMaxSessionDuration:          2 * time.Minute,
		WSMaxSessionsPerPrincipal:     4,
		UpstreamConnectTimeout:        10 * time.Second,
		UpstreamResponseHeaderTimeout: 45 * time.Second,
		ReadHeaderTimeout:             10 * time.Second,
		ReadTimeout:                   45 * time.Second,
		HandlerTimeout:                2 * time.Minute,
		ShutdownGracePeriod:           5 * time.Second,
		LimitRPS:                      10,
		LimitBurst:                    20,
		LimitMaxConcurrentRequests:    20,
		LimitMaxConcurrentStreams:     10,
		TavilyBaseURL:                 "https://api.tavily.com",
		ExaBaseURL:                    "https://api.exa.ai",
		FirecrawlBaseURL:              "https://api.firecrawl.dev",
	}

	logOutput := io.Discard
	if testing.Verbose() {
		logOutput = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(logOutput, nil))
	gw := gatewayserver.NewWithOptions(cfg, logger, opts)
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	client := NewClient(
		WithBaseURL(srv.URL),
		WithProviderKey("openai", openAIKey),
	)

	chain, err := client.Chains.Connect(ctx, &ChainRequest{
		Transport:         TransportWebSocket,
		ExternalSessionID: "smoke-ws-real-" + time.Now().UTC().Format("20060102150405"),
		Model:             "oai-resp/gpt-5-mini",
		System:            "Reply tersely.",
	})
	if err != nil {
		t.Fatalf("Chains.Connect() error = %v", err)
	}
	defer func() { _ = chain.Close() }()

	result, err := chain.Run(ctx, &ChainRunRequest{
		Input: []ContentBlock{
			Text("Reply with the single word READY."),
		},
	})
	if err != nil {
		t.Fatalf("Chain.Run() error = %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatalf("Chain.Run() returned nil result: %#v", result)
	}
	if got := strings.TrimSpace(result.Response.TextContent()); got != "READY" {
		t.Fatalf("response text = %q, want %q", got, "READY")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/chains/"+chain.ID()+"/runs", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /v1/chains/{id}/runs error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /v1/chains/{id}/runs status = %d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var runList struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runList); err != nil {
		t.Fatalf("decode run list: %v", err)
	}
	if len(runList.Items) == 0 {
		t.Fatal("expected at least one persisted run")
	}
	if runList.Items[len(runList.Items)-1].Status != "completed" {
		t.Fatalf("last persisted run status = %q, want completed", runList.Items[len(runList.Items)-1].Status)
	}
}

func loadEnvForSmoke(t *testing.T) {
	t.Helper()
	if err := dotenv.LoadFile(".env"); err != nil && !os.IsNotExist(err) {
		t.Fatalf("LoadFile(.env) error = %v", err)
	}
}

func switchToRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, ".."))
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("Chdir(%q) error = %v", repoRoot, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	return repoRoot
}
