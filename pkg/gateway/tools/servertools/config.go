package servertools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ProviderTavily    = "tavily"
	ProviderExa       = "exa"
	ProviderFirecrawl = "firecrawl"

	DefaultSearchMaxResults = 5
	HardSearchMaxResults    = 10
)

const (
	HeaderProviderKeyTavily    = "X-Provider-Key-Tavily"
	HeaderProviderKeyExa       = "X-Provider-Key-Exa"
	HeaderProviderKeyFirecrawl = "X-Provider-Key-Firecrawl"
)

type WebSearchConfig struct {
	Provider       string   `json:"provider,omitempty"`
	MaxResults     int      `json:"max_results,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
	Country        string   `json:"country,omitempty"`
	Language       string   `json:"language,omitempty"`
}

type WebFetchConfig struct {
	Provider       string   `json:"provider,omitempty"`
	Format         string   `json:"format,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

func DecodeWebSearchConfig(raw any) (WebSearchConfig, error) {
	var cfg WebSearchConfig
	if err := decodeConfigObject(raw, &cfg); err != nil {
		return WebSearchConfig{}, err
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch cfg.Provider {
	case "", ProviderTavily, ProviderExa:
	default:
		return WebSearchConfig{}, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
	cfg.AllowedDomains = normalizeDomains(cfg.AllowedDomains)
	cfg.BlockedDomains = normalizeDomains(cfg.BlockedDomains)
	return cfg, nil
}

func DecodeWebFetchConfig(raw any) (WebFetchConfig, error) {
	var cfg WebFetchConfig
	if err := decodeConfigObject(raw, &cfg); err != nil {
		return WebFetchConfig{}, err
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch cfg.Provider {
	case "", ProviderFirecrawl, ProviderTavily:
	default:
		return WebFetchConfig{}, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
	cfg.Format = strings.ToLower(strings.TrimSpace(cfg.Format))
	switch cfg.Format {
	case "", "text", "markdown", "html":
	default:
		return WebFetchConfig{}, fmt.Errorf("unsupported format %q", cfg.Format)
	}
	cfg.AllowedDomains = normalizeDomains(cfg.AllowedDomains)
	cfg.BlockedDomains = normalizeDomains(cfg.BlockedDomains)
	return cfg, nil
}

func ProviderHeaderName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderTavily:
		return HeaderProviderKeyTavily
	case ProviderExa:
		return HeaderProviderKeyExa
	case ProviderFirecrawl:
		return HeaderProviderKeyFirecrawl
	default:
		return ""
	}
}

func decodeConfigObject(raw any, out any) error {
	if raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	return nil
}
