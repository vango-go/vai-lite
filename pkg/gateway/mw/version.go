package mw

import (
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
)

const (
	apiVersionHeader    = "X-VAI-Version"
	supportedAPIVersion = "1"
)

func APIVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldValidateAPIVersion(r) {
			next.ServeHTTP(w, r)
			return
		}

		versions := parseHeaderCSVValues(r.Header.Values(apiVersionHeader))
		if len(versions) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		for _, version := range versions {
			if version != supportedAPIVersion {
				reqID, _ := RequestIDFrom(r.Context())
				writeJSONError(w, http.StatusBadRequest, &core.Error{
					Type:      core.ErrInvalidRequest,
					Message:   "unsupported API version",
					Param:     apiVersionHeader,
					Code:      "unsupported_version",
					RequestID: reqID,
				})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func shouldValidateAPIVersion(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return false
	}
	if isWebSocketUpgrade(r) {
		return false
	}
	return isV1Path(r.URL.Path)
}

func isV1Path(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !headerHasToken(r.Header, "Connection", "upgrade") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, value := range h.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func parseHeaderCSVValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			out = append(out, trimmed)
		}
	}
	return out
}
