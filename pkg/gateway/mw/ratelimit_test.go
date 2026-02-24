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

func ratelimitConfigNoRPS(maxConcurrent int, maxStreams int) ratelimit.Config {
	return ratelimit.Config{
		RPS:                   0,
		Burst:                 0,
		MaxConcurrentRequests: maxConcurrent,
		MaxConcurrentStreams:  maxStreams,
	}
}
