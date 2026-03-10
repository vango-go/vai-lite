package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/vango-go/vai-lite/internal/blobstore"
	"github.com/vango-go/vai-lite/internal/db"
	"github.com/vango-go/vai-lite/internal/dotenv"
	"github.com/vango-go/vai-lite/pkg/core/errorfmt"
	assetsvc "github.com/vango-go/vai-lite/pkg/gateway/assets"
	chainrt "github.com/vango-go/vai-lite/pkg/gateway/chains"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	gatewayserver "github.com/vango-go/vai-lite/pkg/gateway/server"
)

type proxyDeps struct {
	loadConfig   func() (config.Config, error)
	newGateway   func(context.Context, config.Config, *slog.Logger) (*gatewayserver.Server, error)
	signalNotify func(chan<- os.Signal, ...os.Signal)
	signalStop   func(chan<- os.Signal)
}

func defaultProxyDeps() proxyDeps {
	return proxyDeps{
		loadConfig: config.LoadFromEnv,
		newGateway: func(ctx context.Context, cfg config.Config, logger *slog.Logger) (*gatewayserver.Server, error) {
			var opts gatewayserver.Options
			blob, err := blobstore.Build(ctx)
			if err != nil {
				return nil, fmt.Errorf("build blob store: %w", err)
			}
			if pooled := strings.TrimSpace(os.Getenv("DATABASE_URL")); pooled != "" {
				pool, err := db.ConnectPool(ctx)
				if err != nil {
					return nil, fmt.Errorf("connect database: %w", err)
				}
				opts.ChainStore = chainrt.NewPostgresStore(pool)
				if blob != nil {
					opts.AssetService = assetsvc.NewService(pool, blob, nil, assetsvc.Config{
						StorageProvider: blobstore.ProviderName(),
					})
				}
			}
			return gatewayserver.NewWithOptions(cfg, logger, opts), nil
		},
		signalNotify: func(c chan<- os.Signal, sig ...os.Signal) {
			signal.Notify(c, sig...)
		},
		signalStop: signal.Stop,
	}
}

func buildHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
	}
}

func runProxy(ctx context.Context, logger *slog.Logger, deps proxyDeps) error {
	if deps.loadConfig == nil {
		return errors.New("missing loadConfig dependency")
	}
	if deps.newGateway == nil {
		return errors.New("missing newGateway dependency")
	}
	if deps.signalNotify == nil || deps.signalStop == nil {
		return errors.New("missing signal dependency")
	}
	if logger == nil {
		logger = slog.Default()
	}

	if directURL := strings.TrimSpace(os.Getenv("DATABASE_URL_DIRECT")); directURL != "" {
		if err := db.MigrateUp(ctx, directURL); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
	}

	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	gw, err := deps.newGateway(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("build gateway: %w", err)
	}
	httpSrv := buildHTTPServer(cfg, gw.Handler())

	logger.Info("starting gateway", "addr", cfg.Addr, "auth_mode", cfg.AuthMode)

	listenErrCh := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErrCh <- err
			return
		}
		listenErrCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	deps.signalNotify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer deps.signalStop(sigCh)

	select {
	case err := <-listenErrCh:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case sig := <-sigCh:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	gw.SetDraining()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	if err := <-listenErrCh; err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	logger.Info("gateway stopped")
	return nil
}

func runMain(ctx context.Context, stderr io.Writer, deps proxyDeps) int {
	if stderr == nil {
		stderr = os.Stderr
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))

	if err := dotenv.LoadFile(".env"); err != nil {
		fmt.Fprintf(stderr, "vai-proxy: %s\n", errorfmt.Format(err))
		return 1
	}

	if err := runProxy(ctx, logger, deps); err != nil {
		fmt.Fprintf(stderr, "vai-proxy: %s\n", errorfmt.Format(err))
		return 1
	}
	return 0
}

func main() {
	os.Exit(runMain(context.Background(), os.Stderr, defaultProxyDeps()))
}
