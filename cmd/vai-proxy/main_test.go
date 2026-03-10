package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	gatewayserver "github.com/vango-go/vai-lite/pkg/gateway/server"
)

func TestRunMain_ReturnsNonZeroWhenConfigLoadFails(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	exitCode := runMain(context.Background(), &stderr, proxyDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, errors.New("boom")
		},
		newGateway: func(ctx context.Context, cfg config.Config, logger *slog.Logger) (*gatewayserver.Server, error) {
			t.Fatalf("newGateway should not be called when config load fails")
			return nil, nil
		},
		signalNotify: func(c chan<- os.Signal, sig ...os.Signal) {},
		signalStop:   func(c chan<- os.Signal) {},
	})

	if exitCode != 1 {
		t.Fatalf("exitCode=%d, want 1", exitCode)
	}
	if got := stderr.String(); got == "" {
		t.Fatalf("expected stderr output for startup error")
	}
}

func TestRunMain_FormatsStructuredErrors(t *testing.T) {
	t.Parallel()

	retryAfter := 9
	var stderr bytes.Buffer
	exitCode := runMain(context.Background(), &stderr, proxyDeps{
		loadConfig: func() (config.Config, error) {
			return config.Config{}, &core.Error{
				Type:          core.ErrAPI,
				Message:       "internal error",
				Param:         "auth_mode",
				Code:          "INVALID_AUTH_MODE",
				RequestID:     "req_abc",
				RetryAfter:    &retryAfter,
				ProviderError: map[string]any{"reason": "misconfigured"},
			}
		},
		newGateway: func(ctx context.Context, cfg config.Config, logger *slog.Logger) (*gatewayserver.Server, error) {
			t.Fatalf("newGateway should not be called when config load fails")
			return nil, nil
		},
		signalNotify: func(c chan<- os.Signal, sig ...os.Signal) {},
		signalStop:   func(c chan<- os.Signal) {},
	})

	if exitCode != 1 {
		t.Fatalf("exitCode=%d, want 1", exitCode)
	}
	got := stderr.String()
	wantParts := []string{
		"vai-proxy: load config: api_error: internal error",
		"param=auth_mode",
		"code=INVALID_AUTH_MODE",
		"request_id=req_abc",
		"retry_after=9s",
		`provider_error={"reason":"misconfigured"}`,
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestBuildHTTPServer_UsesConfiguredAddress(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Addr:              "127.0.0.1:9999",
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
	}

	srv := buildHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	if srv.Addr != cfg.Addr {
		t.Fatalf("Addr=%q, want %q", srv.Addr, cfg.Addr)
	}
	if srv.ReadHeaderTimeout != cfg.ReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout=%v, want %v", srv.ReadHeaderTimeout, cfg.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != cfg.ReadTimeout {
		t.Fatalf("ReadTimeout=%v, want %v", srv.ReadTimeout, cfg.ReadTimeout)
	}
}

func TestGatewayHandlerStack_Smoke(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw := gatewayserver.New(config.Config{
		AuthMode: config.AuthModeDisabled,
		APIKeys:  map[string]struct{}{},

		CORSAllowedOrigins:            map[string]struct{}{},
		ModelAllowlist:                map[string]struct{}{},
		UpstreamConnectTimeout:        time.Second,
		UpstreamResponseHeaderTimeout: time.Second,
		ReadHeaderTimeout:             time.Second,
		ReadTimeout:                   time.Second,
		LimitRPS:                      10,
		LimitBurst:                    20,
		LimitMaxConcurrentRequests:    20,
		LimitMaxConcurrentStreams:     10,
		TavilyBaseURL:                 "https://api.tavily.com",
		ExaBaseURL:                    "https://api.exa.ai",
		FirecrawlBaseURL:              "https://api.firecrawl.dev",
	}, logger)

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want %d", resp.StatusCode, http.StatusOK)
	}
}
