package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/lifecycle"
)

func TestReadyHandler_RequiredAuthEmptyKeys_NotReady(t *testing.T) {
	h := ReadyHandler{Config: config.Config{
		AuthMode: config.AuthModeRequired,
		APIKeys:  map[string]struct{}{},

		MaxBodyBytes:                  1,
		MaxMessages:                   1,
		MaxTools:                      1,
		MaxTotalTextBytes:             1,
		MaxB64BytesPerBlock:           1,
		MaxB64BytesTotal:              1,
		SSEPingInterval:               time.Second,
		SSEMaxStreamDuration:          time.Minute,
		StreamIdleTimeout:             time.Second,
		WSMaxSessionDuration:          time.Minute,
		WSMaxSessionsPerPrincipal:     1,
		ReadHeaderTimeout:             time.Second,
		ReadTimeout:                   time.Second,
		HandlerTimeout:                time.Second,
		ShutdownGracePeriod:           time.Second,
		UpstreamConnectTimeout:        time.Second,
		UpstreamResponseHeaderTimeout: time.Second,
	}}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected ok=false, got ok=true")
	}
}

func TestReadyHandler_OptionalAuth_Ready(t *testing.T) {
	h := ReadyHandler{Config: config.Config{
		AuthMode: config.AuthModeOptional,
		APIKeys:  map[string]struct{}{},

		MaxBodyBytes:                  1,
		MaxMessages:                   1,
		MaxTools:                      1,
		MaxTotalTextBytes:             1,
		MaxB64BytesPerBlock:           1,
		MaxB64BytesTotal:              1,
		SSEPingInterval:               time.Second,
		SSEMaxStreamDuration:          time.Minute,
		StreamIdleTimeout:             time.Second,
		WSMaxSessionDuration:          time.Minute,
		WSMaxSessionsPerPrincipal:     1,
		ReadHeaderTimeout:             time.Second,
		ReadTimeout:                   time.Second,
		HandlerTimeout:                time.Second,
		ShutdownGracePeriod:           time.Second,
		UpstreamConnectTimeout:        time.Second,
		UpstreamResponseHeaderTimeout: time.Second,
	}}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestReadyHandler_Draining_NotReady(t *testing.T) {
	lc := &lifecycle.Lifecycle{}
	lc.SetDraining(true)

	h := ReadyHandler{
		Config: config.Config{
			AuthMode: config.AuthModeOptional,
			APIKeys:  map[string]struct{}{},

			MaxBodyBytes:                  1,
			MaxMessages:                   1,
			MaxTools:                      1,
			MaxTotalTextBytes:             1,
			MaxB64BytesPerBlock:           1,
			MaxB64BytesTotal:              1,
			SSEPingInterval:               time.Second,
			SSEMaxStreamDuration:          time.Minute,
			StreamIdleTimeout:             time.Second,
			WSMaxSessionDuration:          time.Minute,
			WSMaxSessionsPerPrincipal:     1,
			ReadHeaderTimeout:             time.Second,
			ReadTimeout:                   time.Second,
			HandlerTimeout:                time.Second,
			ShutdownGracePeriod:           time.Second,
			UpstreamConnectTimeout:        time.Second,
			UpstreamResponseHeaderTimeout: time.Second,
		},
		Lifecycle: lc,
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected ok=false, got ok=true")
	}
}
