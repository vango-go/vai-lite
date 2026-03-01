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
	"syscall"

	"github.com/vango-go/vai-lite/internal/dotenv"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	gatewayserver "github.com/vango-go/vai-lite/pkg/gateway/server"
)

type proxyDeps struct {
	loadConfig   func() (config.Config, error)
	newGateway   func(config.Config, *slog.Logger) *gatewayserver.Server
	signalNotify func(chan<- os.Signal, ...os.Signal)
	signalStop   func(chan<- os.Signal)
}

func defaultProxyDeps() proxyDeps {
	return proxyDeps{
		loadConfig: config.LoadFromEnv,
		newGateway: gatewayserver.New,
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

	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	gw := deps.newGateway(cfg, logger)
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
	gw.WarnLiveSessionsDraining()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
	defer waitCancel()
	if !gw.WaitLiveSessions(waitCtx) {
		gw.CancelLiveSessions()
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
		fmt.Fprintf(stderr, "vai-proxy: %v\n", err)
		return 1
	}

	if err := runProxy(ctx, logger, deps); err != nil {
		fmt.Fprintf(stderr, "vai-proxy: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(runMain(context.Background(), os.Stderr, defaultProxyDeps()))
}
