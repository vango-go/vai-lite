package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vango-go/vai-lite/app/components"
	"github.com/vango-go/vai-lite/app/routes"
	"github.com/vango-go/vai-lite/internal/appruntime"
	"github.com/vango-go/vai-lite/internal/db"
	"github.com/vango-go/vai-lite/internal/dotenv"
	"github.com/vango-go/vai-lite/internal/services"
	gatewayconfig "github.com/vango-go/vai-lite/pkg/gateway/config"
	gatewayserver "github.com/vango-go/vai-lite/pkg/gateway/server"
	"github.com/vango-go/vango"
	neon "github.com/vango-go/vango-neon"
	s3store "github.com/vango-go/vango-s3"
	workos "github.com/vango-go/vango-workos"
	wosvault "github.com/workos/workos-go/v6/pkg/vault"
)

type serverConfig struct {
	Addr         string
	BaseURL      string
	DefaultModel string
	DevMode      bool
	TopupOptions []int64
}

type betaServer struct {
	cfg      serverConfig
	logger   *slog.Logger
	db       neon.DB
	services *services.AppServices
	gateway  http.Handler
	workos   *workos.Client
	app      *vango.App
}

func main() {
	if err := dotenv.LoadFile(".env"); err != nil {
		slog.Warn("dotenv load failed", "error", err)
	}

	logger := slog.Default()
	cfg := loadServerConfig()
	ctx := context.Background()

	directURL := strings.TrimSpace(os.Getenv("DATABASE_URL_DIRECT"))
	if directURL != "" {
		if err := db.MigrateUp(ctx, directURL); err != nil {
			logger.Error("database migration failed", "error", err)
			os.Exit(1)
		}
	}
	if envOr("APP_MIGRATE_ONLY", "") == "1" {
		logger.Info("database migrations complete")
		return
	}

	var database neon.DB
	pool, err := db.ConnectPool(ctx)
	if err != nil {
		if cfg.DevMode && strings.Contains(err.Error(), "DATABASE_URL is required") {
			logger.Warn("starting in degraded dev mode without database")
			database = &neon.TestDB{}
		} else {
			logger.Error("database connect failed", "error", err)
			os.Exit(1)
		}
	} else {
		database = pool
	}

	gwCfg, err := loadGatewayConfigForMonolith()
	if err != nil {
		logger.Error("gateway config load failed", "error", err)
		os.Exit(1)
	}
	gw := gatewayserver.New(gwCfg, logger)

	workosClient, bridge, err := buildWorkOSClient(cfg.BaseURL)
	if err != nil {
		logger.Error("workos config invalid", "error", err)
		os.Exit(1)
	}

	blobStore, err := buildBlobStore(ctx)
	if err != nil {
		logger.Error("blob store config invalid", "error", err)
		os.Exit(1)
	}

	var vaultClient *wosvault.Client
	if apiKey := strings.TrimSpace(os.Getenv("WORKOS_API_KEY")); apiKey != "" {
		vaultClient = &wosvault.Client{APIKey: apiKey}
	}

	appServices := services.New(
		database,
		cfg.BaseURL,
		cfg.DefaultModel,
		strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")),
		strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET")),
		nil,
		cfg.TopupOptions,
		vaultClient,
		blobStore,
	)

	appCfg := vango.Config{
		Static: vango.StaticConfig{
			Dir:    "public",
			Prefix: "/",
		},
		SSR: vango.SSRConfig{
			// Current page handlers still perform durable reads during SSR.
			// Vango defaults SSR resource timeout to 300ms, which is too small
			// for this app's current chat/settings loads and causes false timeout
			// failures before the live session takes over.
			ResourceTimeout: durationPtr(3 * time.Second),
		},
		DevMode: cfg.DevMode,
	}
	if bridge != nil {
		appCfg.OnSessionStart = bridge.OnSessionStart
		appCfg.OnSessionResume = bridge.OnSessionResume
		appCfg.Session.AuthCheck = workosClient.RevalidationConfig()
	}

	app, err := vango.New(appCfg)
	if err != nil {
		logger.Error("vango app init failed", "error", err)
		os.Exit(1)
	}
	if cfg.DevMode {
		// Temporary app-level workaround for a Vango config translation bug:
		// vango.Config.DebugMode currently does not flow into server.ServerConfig.
		// Force it here so websocket close reasons and client self-heal paths are debuggable.
		app.Server().Config().DebugMode = true
		if app.Server().Config().SessionConfig != nil {
			app.Server().Config().SessionConfig.DebugMode = true
		}
		app.Server().SetLogger(logger)
	}

	server := &betaServer{
		cfg:      cfg,
		logger:   logger,
		db:       database,
		services: appServices,
		gateway:  gw.Handler(),
		workos:   workosClient,
		app:      app,
	}
	appruntime.Set(&appruntime.Deps{
		Services: appServices,
		Gateway:  gw.Handler(),
		Logger:   logger,
		Config: appruntime.Config{
			BaseURL:       cfg.BaseURL,
			DefaultModel:  cfg.DefaultModel,
			TopupOptions:  cfg.TopupOptions,
			WorkOSEnabled: workosClient != nil,
			DevMode:       cfg.DevMode,
		},
	})
	routes.Register(app)
	app.SetNotFound(func(ctx vango.Ctx) *vango.VNode {
		return components.NotFoundPage()
	})
	if workosClient != nil {
		app.Server().Use(workosClient.Middleware())
	}

	mux := http.NewServeMux()
	server.registerMux(mux)
	app.Server().SetHandler(mux)

	logger.Info("starting vai-lite beta server", "addr", cfg.Addr, "base_url", cfg.BaseURL)
	if err := app.RunAddr(cfg.Addr); err != nil {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func loadServerConfig() serverConfig {
	baseURL := strings.TrimSpace(os.Getenv("APP_BASE_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("WORKOS_BASE_URL"))
	}
	if baseURL == "" {
		if envOr("VANGO_DEV", "") == "1" {
			baseURL = "http://localhost:8000"
		} else {
			baseURL = "http://localhost:8080"
		}
	}
	addr := strings.TrimSpace(os.Getenv("APP_ADDR"))
	if addr == "" {
		if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
			addr = ":" + port
		}
	}
	if addr == "" {
		addr = ":8080"
	}
	return serverConfig{
		Addr:         addr,
		BaseURL:      strings.TrimRight(baseURL, "/"),
		DefaultModel: envOr("APP_DEFAULT_MODEL", "oai-resp/gpt-5-mini"),
		DevMode:      envOr("VANGO_DEV", "") == "1",
		TopupOptions: parseInt64CSV(envOr("APP_TOPUP_OPTIONS", "1000,2500,5000")),
	}
}

func buildWorkOSClient(baseURL string) (*workos.Client, *workos.Bridge, error) {
	apiKey := strings.TrimSpace(os.Getenv("WORKOS_API_KEY"))
	clientID := strings.TrimSpace(os.Getenv("WORKOS_CLIENT_ID"))
	cookieSecret := strings.TrimSpace(os.Getenv("WORKOS_COOKIE_SECRET"))
	if apiKey == "" || clientID == "" || cookieSecret == "" {
		return nil, nil, nil
	}

	redirectURI := strings.TrimSpace(os.Getenv("WORKOS_REDIRECT_URI"))
	if redirectURI == "" {
		redirectURI = strings.TrimRight(baseURL, "/") + "/auth/callback"
	}

	client, err := workos.New(workos.Config{
		APIKey:                apiKey,
		ClientID:              clientID,
		RedirectURI:           redirectURI,
		SignOutRedirectURI:    "/auth/signed-out",
		CookieSecret:          cookieSecret,
		CookieSecretFallbacks: splitCSV(os.Getenv("WORKOS_COOKIE_SECRET_FALLBACKS")),
		BaseURL:               baseURL,
		JWTIssuer:             strings.TrimSpace(os.Getenv("WORKOS_JWT_ISSUER")),
		JWTAudience:           strings.TrimSpace(os.Getenv("WORKOS_JWT_AUDIENCE")),
		WebhookSecret:         strings.TrimSpace(os.Getenv("WORKOS_WEBHOOK_SECRET")),
		EnableAuditLogs:       true,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, client.SessionBridge(), nil
}

func buildBlobStore(ctx context.Context) (s3store.Store, error) {
	accessKey := strings.TrimSpace(os.Getenv("S3_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("S3_SECRET_ACCESS_KEY"))
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
	intentSecret := strings.TrimSpace(os.Getenv("S3_INTENT_SECRET"))
	if accessKey == "" || secretKey == "" || bucket == "" || intentSecret == "" {
		return nil, nil
	}

	if accountID := strings.TrimSpace(os.Getenv("R2_ACCOUNT_ID")); accountID != "" {
		return s3store.NewR2(ctx, s3store.R2Config{
			AccountID:       accountID,
			AccessKeyID:     accessKey,
			SecretAccessKey: secretKey,
			Bucket:          bucket,
			IntentSecret:    intentSecret,
		})
	}

	return s3store.New(ctx, s3store.Config{
		Endpoint:        strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		Bucket:          bucket,
		Region:          envOr("S3_REGION", "auto"),
		IntentSecret:    intentSecret,
	})
}

func loadGatewayConfigForMonolith() (gatewayconfig.Config, error) {
	restore := false
	prev := os.Getenv("VAI_PROXY_AUTH_MODE")
	if strings.TrimSpace(prev) == "" {
		restore = true
		_ = os.Setenv("VAI_PROXY_AUTH_MODE", string(gatewayconfig.AuthModeDisabled))
	}
	cfg, err := gatewayconfig.LoadFromEnv()
	if restore {
		_ = os.Unsetenv("VAI_PROXY_AUTH_MODE")
	} else {
		_ = os.Setenv("VAI_PROXY_AUTH_MODE", prev)
	}
	if err != nil {
		return gatewayconfig.Config{}, err
	}
	cfg.AuthMode = gatewayconfig.AuthModeDisabled
	cfg.APIKeys = map[string]struct{}{}
	return cfg, nil
}

func durationPtr(d time.Duration) *time.Duration {
	return &d
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseInt64CSV(raw string) []int64 {
	parts := splitCSV(raw)
	if len(parts) == 0 {
		return nil
	}
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.ParseInt(part, 10, 64)
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}
