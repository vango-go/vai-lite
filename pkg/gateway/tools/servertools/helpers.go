package servertools

import (
	"fmt"
	"net/url"
	"strings"
)

func normalizeDomains(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, domain := range input {
		value := strings.ToLower(strings.TrimSpace(domain))
		value = strings.TrimPrefix(value, ".")
		value = strings.TrimSuffix(value, ".")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func urlHost(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return "", fmt.Errorf("missing host")
	}
	host = strings.TrimSuffix(host, ".")
	return host, nil
}

func hostMatchesDomain(host, domain string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if host == "" || domain == "" {
		return false
	}
	host = strings.TrimSuffix(host, ".")
	domain = strings.TrimPrefix(strings.TrimSuffix(domain, "."), ".")
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func hostAllowed(host string, allowed, blocked []string) bool {
	if len(allowed) > 0 {
		matched := false
		for _, domain := range allowed {
			if hostMatchesDomain(host, domain) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, domain := range blocked {
		if hostMatchesDomain(host, domain) {
			return false
		}
	}
	return true
}

func intFromAny(v any) (int, bool) {
	switch value := v.(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func truncateString(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	if maxLen <= 3 {
		return value[:maxLen]
	}
	return value[:maxLen-3] + "..."
}
