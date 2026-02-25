package mw

import (
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

var corsAllowedMethods = "GET, POST, OPTIONS"

var corsAllowedHeaderAllowlist = map[string]struct{}{
	"Authorization":             {},
	"Content-Type":              {},
	"X-VAI-Version":             {},
	"X-Provider-Key-Anthropic":  {},
	"X-Provider-Key-OpenAI":     {},
	"X-Provider-Key-Gemini":     {},
	"X-Provider-Key-Groq":       {},
	"X-Provider-Key-Cerebras":   {},
	"X-Provider-Key-OpenRouter": {},
	"X-Provider-Key-Cartesia":   {},
	"X-Provider-Key-ElevenLabs": {},
}

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

			requestedHeaders := parseHeaderList(strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")))
			allowedHeaders := filterAllowedHeaders(requestedHeaders)

			w.Header().Set("Access-Control-Allow-Origin", origin)
			addVary(w, "Origin", "Access-Control-Request-Headers")
			w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
			if len(allowedHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
			}
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Non-preflight: only attach CORS headers when explicitly allowlisted.
		if origin != "" && len(allowed) > 0 {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				addVary(w, "Origin")
				w.Header().Set("Access-Control-Expose-Headers", corsExposedHeaders)
			}
		}

		next.ServeHTTP(w, r)
	})
}

func filterAllowedHeaders(requested []string) []string {
	if len(requested) == 0 {
		return nil
	}
	out := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, h := range requested {
		key := strings.ToLower(strings.TrimSpace(h))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		if headerAllowed(h) {
			out = append(out, strings.TrimSpace(h))
			seen[key] = struct{}{}
		}
	}
	return out
}

func headerAllowed(header string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	for allowed := range corsAllowedHeaderAllowlist {
		if strings.EqualFold(allowed, header) {
			return true
		}
	}
	return false
}

func parseHeaderList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func addVary(w http.ResponseWriter, values ...string) {
	if w == nil || len(values) == 0 {
		return
	}
	existing := parseHeaderList(w.Header().Get("Vary"))
	seen := make(map[string]struct{}, len(existing))
	for _, v := range existing {
		seen[strings.ToLower(v)] = struct{}{}
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		existing = append(existing, v)
		seen[key] = struct{}{}
	}
	if len(existing) > 0 {
		w.Header().Set("Vary", strings.Join(existing, ", "))
	}
}
