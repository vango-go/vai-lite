package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

func TestAuth_RequiredRejectsMissingBearer(t *testing.T) {
	h := Auth(config.Config{AuthMode: config.AuthModeRequired, APIKeys: map[string]struct{}{"vai_sk_test": {}}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestAuth_LiveWebSocketUpgradeBypass(t *testing.T) {
	h := Auth(config.Config{AuthMode: config.AuthModeRequired, APIKeys: map[string]struct{}{"vai_sk_test": {}}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/live", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}
