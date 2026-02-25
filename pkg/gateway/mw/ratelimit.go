package mw

import (
	"net/http"
	"strconv"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/principal"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
)

func RateLimit(cfg config.Config, limiter *ratelimit.Limiter, next http.Handler) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health endpoints must remain cheap and reliable.
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		p := principal.Resolve(r, cfg)

		dec := limiter.AcquireRequest(p.Key, time.Now())
		if !dec.Allowed {
			reqID, _ := RequestIDFrom(r.Context())
			if dec.RetryAfter > 0 {
				w.Header().Set("Retry-After", itoa(dec.RetryAfter))
			}
			writeJSONError(w, http.StatusTooManyRequests, &core.Error{
				Type:      core.ErrRateLimit,
				Message:   "rate limit exceeded",
				RequestID: reqID,
				RetryAfter: func() *int {
					if dec.RetryAfter <= 0 {
						return nil
					}
					v := dec.RetryAfter
					return &v
				}(),
			})
			return
		}
		if dec.Permit != nil {
			defer dec.Permit.Release()
		}

		next.ServeHTTP(w, r)
	})
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
