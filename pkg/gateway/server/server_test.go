package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

func TestServer_UnknownRoute_ReturnsJSON404(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	s := New(config.Config{
		AuthMode: config.AuthModeDisabled,
		APIKeys:  map[string]struct{}{},

		CORSAllowedOrigins:            map[string]struct{}{},
		ModelAllowlist:                map[string]struct{}{},
		UpstreamConnectTimeout:        time.Second,
		UpstreamResponseHeaderTimeout: time.Second,
	}, logger)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type=%q", ct)
	}
	if !strings.Contains(rr.Body.String(), `"type":"not_found_error"`) {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
}

func TestServer_ModelsRoute_Reachable(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	s := New(config.Config{
		AuthMode: config.AuthModeDisabled,
		APIKeys:  map[string]struct{}{},

		CORSAllowedOrigins:            map[string]struct{}{},
		ModelAllowlist:                map[string]struct{}{},
		UpstreamConnectTimeout:        time.Second,
		UpstreamResponseHeaderTimeout: time.Second,
	}, logger)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type=%q", ct)
	}
	if !strings.Contains(rr.Body.String(), `"models"`) {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
}
