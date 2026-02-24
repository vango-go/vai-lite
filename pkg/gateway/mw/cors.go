package mw

import (
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

var corsAllowedMethods = "GET, POST, OPTIONS"

var corsAllowedHeaders = strings.Join([]string{
	"Authorization",
	"Content-Type",
	"X-Request-ID",
	"Idempotency-Key",
	"X-Provider-Key-Anthropic",
	"X-Provider-Key-OpenAI",
	"X-Provider-Key-Gemini",
	"X-Provider-Key-Groq",
	"X-Provider-Key-Cerebras",
	"X-Provider-Key-OpenRouter",
	"X-Provider-Key-Cartesia",
	"X-Provider-Key-ElevenLabs",
}, ", ")

var corsExposedHeaders = strings.Join([]string{
	"X-Request-ID",
	"X-Model",
	"X-Input-Tokens",
	"X-Output-Tokens",
	"X-Total-Tokens",
	"X-Cost-USD",
	"X-Duration-Ms",
}, ", ")

func CORS(cfg config.Config, next http.Handler) http.Handler {
	allowed := cfg.CORSAllowedOrigins
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))

		// Preflight: explicitly allow/deny so browser callers get deterministic behavior.
		if r.Method == http.MethodOptions && strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")) != "" {
			if origin == "" || len(allowed) == 0 {
				http.Error(w, "cors preflight not allowed", http.StatusForbidden)
				return
			}
			if _, ok := allowed[origin]; !ok {
				http.Error(w, "cors preflight not allowed", http.StatusForbidden)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Non-preflight: only attach CORS headers when explicitly allowlisted.
		if origin != "" && len(allowed) > 0 {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Expose-Headers", corsExposedHeaders)
			}
		}

		next.ServeHTTP(w, r)
	})
}
