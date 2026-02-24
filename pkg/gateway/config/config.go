package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type AuthMode string

const (
	AuthModeRequired AuthMode = "required"
	AuthModeOptional AuthMode = "optional"
	AuthModeDisabled AuthMode = "disabled"
)

type Config struct {
	Addr string

	AuthMode AuthMode
	APIKeys  map[string]struct{}

	MaxBodyBytes int64

	// Request-shape limits (enforced after strict decode).
	MaxMessages       int
	MaxTools          int
	MaxTotalTextBytes int64

	// Base64 budgets (decoded bytes) enforced before expensive decode work.
	MaxB64BytesPerBlock int64
	MaxB64BytesTotal    int64

	// Optional allowlist of canonical model IDs (provider/model).
	ModelAllowlist map[string]struct{}

	// CORS
	CORSAllowedOrigins map[string]struct{} // empty => disabled

	// SSE
	SSEPingInterval      time.Duration
	SSEMaxStreamDuration time.Duration

	// In-memory limits (per principal).
	LimitRPS                   float64
	LimitBurst                 int
	LimitMaxConcurrentRequests int
	LimitMaxConcurrentStreams  int

	// Operational defaults
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	HandlerTimeout    time.Duration

	// Upstream HTTP client defaults
	UpstreamResponseHeaderTimeout time.Duration
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		Addr:                          envOr("VAI_GATEWAY_ADDR", ":8080"),
		AuthMode:                      AuthMode(envOr("VAI_AUTH_MODE", string(AuthModeRequired))),
		APIKeys:                       make(map[string]struct{}),
		MaxBodyBytes:                  envInt64Or("VAI_MAX_BODY_BYTES", 25<<20), // 25 MiB
		MaxMessages:                   envIntOr("VAI_MAX_MESSAGES", 64),
		MaxTools:                      envIntOr("VAI_MAX_TOOLS", 64),
		MaxTotalTextBytes:             envInt64Or("VAI_MAX_TOTAL_TEXT_BYTES", 512<<10),  // 512 KiB
		MaxB64BytesPerBlock:           envInt64Or("VAI_MAX_B64_BYTES_PER_BLOCK", 4<<20), // 4 MiB decoded
		MaxB64BytesTotal:              envInt64Or("VAI_MAX_B64_BYTES_TOTAL", 12<<20),    // 12 MiB decoded
		ModelAllowlist:                make(map[string]struct{}),
		CORSAllowedOrigins:            make(map[string]struct{}),
		SSEPingInterval:               envDurationOr("VAI_SSE_PING_INTERVAL", 15*time.Second),
		SSEMaxStreamDuration:          envDurationOr("VAI_SSE_MAX_STREAM_DURATION", 10*time.Minute),
		LimitRPS:                      envFloat64Or("VAI_LIMIT_RPS", 2.0),
		LimitBurst:                    envIntOr("VAI_LIMIT_BURST", 4),
		LimitMaxConcurrentRequests:    envIntOr("VAI_LIMIT_MAX_CONCURRENT_REQUESTS", 20),
		LimitMaxConcurrentStreams:     envIntOr("VAI_LIMIT_MAX_CONCURRENT_STREAMS", 4),
		ReadHeaderTimeout:             envDurationOr("VAI_READ_HEADER_TIMEOUT", 10*time.Second),
		ReadTimeout:                   envDurationOr("VAI_READ_TIMEOUT", 30*time.Second),
		HandlerTimeout:                envDurationOr("VAI_HANDLER_TIMEOUT", 2*time.Minute),
		UpstreamResponseHeaderTimeout: envDurationOr("VAI_UPSTREAM_RESPONSE_HEADER_TIMEOUT", 30*time.Second),
	}

	switch cfg.AuthMode {
	case AuthModeRequired, AuthModeOptional, AuthModeDisabled:
	default:
		return Config{}, fmt.Errorf("VAI_AUTH_MODE must be one of required|optional|disabled")
	}

	for _, key := range splitCSV(os.Getenv("VAI_API_KEYS")) {
		cfg.APIKeys[key] = struct{}{}
	}

	for _, m := range splitCSV(os.Getenv("VAI_MODEL_ALLOWLIST")) {
		cfg.ModelAllowlist[m] = struct{}{}
	}

	for _, origin := range splitCSV(os.Getenv("VAI_CORS_ALLOWED_ORIGINS")) {
		cfg.CORSAllowedOrigins[origin] = struct{}{}
	}

	if cfg.MaxBodyBytes <= 0 {
		return Config{}, fmt.Errorf("VAI_MAX_BODY_BYTES must be > 0")
	}

	if cfg.MaxMessages <= 0 {
		return Config{}, fmt.Errorf("VAI_MAX_MESSAGES must be > 0")
	}
	if cfg.MaxTools <= 0 {
		return Config{}, fmt.Errorf("VAI_MAX_TOOLS must be > 0")
	}
	if cfg.MaxTotalTextBytes <= 0 {
		return Config{}, fmt.Errorf("VAI_MAX_TOTAL_TEXT_BYTES must be > 0")
	}
	if cfg.MaxB64BytesPerBlock <= 0 {
		return Config{}, fmt.Errorf("VAI_MAX_B64_BYTES_PER_BLOCK must be > 0")
	}
	if cfg.MaxB64BytesTotal <= 0 {
		return Config{}, fmt.Errorf("VAI_MAX_B64_BYTES_TOTAL must be > 0")
	}
	if cfg.MaxB64BytesPerBlock > cfg.MaxB64BytesTotal {
		return Config{}, fmt.Errorf("VAI_MAX_B64_BYTES_PER_BLOCK must be <= VAI_MAX_B64_BYTES_TOTAL")
	}
	if cfg.SSEPingInterval <= 0 {
		return Config{}, fmt.Errorf("VAI_SSE_PING_INTERVAL must be > 0")
	}
	if cfg.SSEMaxStreamDuration <= 0 {
		return Config{}, fmt.Errorf("VAI_SSE_MAX_STREAM_DURATION must be > 0")
	}
	if cfg.ReadHeaderTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_READ_HEADER_TIMEOUT must be > 0")
	}
	if cfg.ReadTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_READ_TIMEOUT must be > 0")
	}
	if cfg.HandlerTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_HANDLER_TIMEOUT must be > 0")
	}
	if cfg.UpstreamResponseHeaderTimeout <= 0 {
		return Config{}, fmt.Errorf("VAI_UPSTREAM_RESPONSE_HEADER_TIMEOUT must be > 0")
	}

	if cfg.LimitRPS < 0 {
		return Config{}, fmt.Errorf("VAI_LIMIT_RPS must be >= 0")
	}
	if cfg.LimitBurst < 0 {
		return Config{}, fmt.Errorf("VAI_LIMIT_BURST must be >= 0")
	}
	if cfg.LimitMaxConcurrentRequests < 0 {
		return Config{}, fmt.Errorf("VAI_LIMIT_MAX_CONCURRENT_REQUESTS must be >= 0")
	}
	if cfg.LimitMaxConcurrentStreams < 0 {
		return Config{}, fmt.Errorf("VAI_LIMIT_MAX_CONCURRENT_STREAMS must be >= 0")
	}

	if cfg.AuthMode == AuthModeRequired && len(cfg.APIKeys) == 0 {
		return Config{}, fmt.Errorf("VAI_API_KEYS must be set when VAI_AUTH_MODE=required")
	}

	return cfg, nil
}

func envOr(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func envInt64Or(key string, def int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func envIntOr(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func envFloat64Or(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def
	}
	return n
}

func envDurationOr(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return def
	}
	return d
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
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
