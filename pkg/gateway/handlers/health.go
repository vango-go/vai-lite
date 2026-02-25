package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
)

type HealthHandler struct{}

func (h HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

type ReadyHandler struct {
	Config config.Config
}

func (h ReadyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	type readyResp struct {
		OK               bool     `json:"ok"`
		AuthMode         string   `json:"auth_mode"`
		AllowlistEnabled bool     `json:"allowlist_enabled"`
		LimitsEnabled    bool     `json:"limits_enabled"`
		Issues           []string `json:"issues,omitempty"`
	}

	issues := make([]string, 0, 4)

	switch h.Config.AuthMode {
	case config.AuthModeRequired, config.AuthModeOptional, config.AuthModeDisabled:
	default:
		issues = append(issues, "invalid auth_mode")
	}
	if h.Config.AuthMode == config.AuthModeRequired && len(h.Config.APIKeys) == 0 {
		issues = append(issues, "auth_mode=required but no api keys configured")
	}

	if h.Config.MaxBodyBytes <= 0 {
		issues = append(issues, "max_body_bytes must be > 0")
	}
	if h.Config.MaxMessages <= 0 {
		issues = append(issues, "max_messages must be > 0")
	}
	if h.Config.MaxTools <= 0 {
		issues = append(issues, "max_tools must be > 0")
	}
	if h.Config.MaxTotalTextBytes <= 0 {
		issues = append(issues, "max_total_text_bytes must be > 0")
	}
	if h.Config.MaxB64BytesPerBlock <= 0 || h.Config.MaxB64BytesTotal <= 0 {
		issues = append(issues, "base64 budgets must be > 0")
	}
	if h.Config.MaxB64BytesPerBlock > h.Config.MaxB64BytesTotal {
		issues = append(issues, "base64 per-block budget must be <= total budget")
	}
	if h.Config.SSEPingInterval <= 0 {
		issues = append(issues, "sse ping interval must be > 0")
	}
	if h.Config.SSEMaxStreamDuration <= 0 {
		issues = append(issues, "sse max stream duration must be > 0")
	}
	if h.Config.StreamIdleTimeout <= 0 {
		issues = append(issues, "stream idle timeout must be > 0")
	}
	if h.Config.WSMaxSessionDuration <= 0 {
		issues = append(issues, "ws max session duration must be > 0")
	}
	if h.Config.WSMaxSessionsPerPrincipal <= 0 {
		issues = append(issues, "ws max sessions per principal must be > 0")
	}
	if h.Config.ReadHeaderTimeout <= 0 || h.Config.ReadTimeout <= 0 || h.Config.HandlerTimeout <= 0 {
		issues = append(issues, "timeouts must be > 0")
	}
	if h.Config.UpstreamConnectTimeout <= 0 || h.Config.UpstreamResponseHeaderTimeout <= 0 {
		issues = append(issues, "upstream timeouts must be > 0")
	}

	allowlistEnabled := len(h.Config.ModelAllowlist) > 0
	limitsEnabled := (h.Config.LimitRPS > 0 && h.Config.LimitBurst > 0) ||
		(h.Config.LimitMaxConcurrentRequests > 0) ||
		(h.Config.LimitMaxConcurrentStreams > 0)

	ok := len(issues) == 0
	status := http.StatusOK
	if !ok {
		status = http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(readyResp{
		OK:               ok,
		AuthMode:         string(h.Config.AuthMode),
		AllowlistEnabled: allowlistEnabled,
		LimitsEnabled:    limitsEnabled,
		Issues:           issues,
	})
}
