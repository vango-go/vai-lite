package mw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/gateway/auth"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

type ctxKeyRequestID struct{}

func RequestIDFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKeyRequestID{}).(string)
	return id, ok && id != ""
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID{}, id)
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = "req_" + randHex(10)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

func Auth(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID, _ := RequestIDFrom(r.Context())

		switch cfg.AuthMode {
		case config.AuthModeDisabled:
			next.ServeHTTP(w, r)
			return
		case config.AuthModeOptional, config.AuthModeRequired:
		default:
			writeJSONError(w, http.StatusInternalServerError, &core.Error{
				Type:      core.ErrAPI,
				Message:   "invalid auth_mode",
				RequestID: reqID,
			})
			return
		}

		token, ok := auth.ParseBearer(r)
		if !ok {
			if cfg.AuthMode == config.AuthModeRequired {
				writeJSONError(w, http.StatusUnauthorized, &core.Error{
					Type:      core.ErrAuthentication,
					Message:   "missing bearer token",
					Param:     "Authorization",
					RequestID: reqID,
				})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := cfg.APIKeys[token]; !ok {
			writeJSONError(w, http.StatusUnauthorized, &core.Error{
				Type:      core.ErrAuthentication,
				Message:   "invalid api key",
				RequestID: reqID,
			})
			return
		}
		p := &auth.Principal{APIKey: token}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

func Recover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if logger != nil {
					logger.Error("panic", "panic", v)
				}
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func AccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if logger == nil {
			return
		}
		reqID, _ := RequestIDFrom(r.Context())
		logger.Info("request",
			"request_id", reqID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func randHex(nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail in practice; fall back to time-based entropy.
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b)
}

type errorEnvelope struct {
	Error *core.Error `json:"error"`
}

func writeJSONError(w http.ResponseWriter, status int, err *core.Error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: err})
}
