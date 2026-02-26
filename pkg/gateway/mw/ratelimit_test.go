package mw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
)

func TestRateLimit_Burst429IncludesRetryAfter(t *testing.T) {
	lim := ratelimit.New(ratelimit.Config{
		RPS:   1,
		Burst: 1,
	})

	h := RateLimit(config.Config{}, lim, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	{
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("first request status=%d body=%q", rr.Code, rr.Body.String())
		}
	}

	{
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("second request status=%d body=%q", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("Retry-After"); got == "" {
			t.Fatalf("expected Retry-After header")
		}
		if body := rr.Body.String(); body == "" || !strings.Contains(body, `"type":"rate_limit_error"`) {
			t.Fatalf("unexpected body: %q", body)
		}
	}
}

func TestRateLimit_ConcurrentRequests429(t *testing.T) {
	lim := ratelimit.New(ratelimitConfigNoRPS(1, 0))

	started := make(chan struct{})
	release := make(chan struct{})

	h := RateLimit(config.Config{}, lim, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("first request status=%d body=%q", rr.Code, rr.Body.String())
		}
	}()

	<-started

	req2 := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status=%d body=%q", rr2.Code, rr2.Body.String())
	}

	close(release)
	wg.Wait()
}

func TestRateLimit_UsesRemoteAddrForUnauthenticatedPrincipal(t *testing.T) {
	lim := ratelimit.New(ratelimit.Config{
		RPS:   1,
		Burst: 1,
	})

	h := RateLimit(config.Config{}, lim, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req1 := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req1.RemoteAddr = "1.1.1.1:1234"
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("req1 status=%d body=%q", rr1.Code, rr1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req2.RemoteAddr = "2.2.2.2:1234"
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("req2 status=%d body=%q", rr2.Code, rr2.Body.String())
	}

	req3 := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req3.RemoteAddr = "1.1.1.1:1234"
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusTooManyRequests {
		t.Fatalf("req3 status=%d body=%q", rr3.Code, rr3.Body.String())
	}
}

func TestRateLimit_TrustProxyHeaders_UsesXForwardedFor(t *testing.T) {
	lim := ratelimit.New(ratelimit.Config{
		RPS:   1,
		Burst: 1,
	})

	h := RateLimit(config.Config{TrustProxyHeaders: true}, lim, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req1 := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	req1.Header.Set("X-Forwarded-For", "1.1.1.1, 10.0.0.1")
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("req1 status=%d body=%q", rr1.Code, rr1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	req2.Header.Set("X-Forwarded-For", "2.2.2.2, 10.0.0.1")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("req2 status=%d body=%q", rr2.Code, rr2.Body.String())
	}
}

func TestRateLimit_LiveWebSocketUpgradeBypass(t *testing.T) {
	lim := ratelimit.New(ratelimit.Config{
		RPS:   1,
		Burst: 1,
	})

	h := RateLimit(config.Config{}, lim, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/v1/live", nil)
	req1.Header.Set("Connection", "Upgrade")
	req1.Header.Set("Upgrade", "websocket")
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusNoContent {
		t.Fatalf("first status=%d body=%q", rr1.Code, rr1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/live", nil)
	req2.Header.Set("Connection", "Upgrade")
	req2.Header.Set("Upgrade", "websocket")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNoContent {
		t.Fatalf("second status=%d body=%q", rr2.Code, rr2.Body.String())
	}
}

func ratelimitConfigNoRPS(maxConcurrent int, maxStreams int) ratelimit.Config {
	return ratelimit.Config{
		RPS:                   0,
		Burst:                 0,
		MaxConcurrentRequests: maxConcurrent,
		MaxConcurrentStreams:  maxStreams,
	}
}
