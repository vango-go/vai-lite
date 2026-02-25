package mw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

func TestCORS_DisabledByDefault_NoHeaders(t *testing.T) {
	h := CORS(config.Config{CORSAllowedOrigins: map[string]struct{}{}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS headers, got Access-Control-Allow-Origin=%q", got)
	}
}

func TestCORS_AllowlistedOrigin_AttachesHeaders(t *testing.T) {
	h := CORS(config.Config{CORSAllowedOrigins: map[string]struct{}{
		"http://localhost:3000": {},
	}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("Access-Control-Allow-Origin=%q", got)
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary=%q", got)
	}
	if got := rr.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Fatalf("expected exposed headers to be set")
	}
}

func TestCORS_Preflight_Allowed(t *testing.T) {
	h := CORS(config.Config{CORSAllowedOrigins: map[string]struct{}{
		"https://app.example.com": {},
	}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for preflight")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/messages", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Access-Control-Allow-Origin=%q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("expected allow-methods header")
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatalf("expected allow-headers header")
	} else if !strings.Contains(got, "X-VAI-Version") {
		t.Fatalf("expected X-VAI-Version in allow-headers, got %q", got)
	}
}

func TestCORS_Preflight_Disallowed(t *testing.T) {
	h := CORS(config.Config{CORSAllowedOrigins: map[string]struct{}{
		"https://app.example.com": {},
	}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for preflight")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/messages", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}
