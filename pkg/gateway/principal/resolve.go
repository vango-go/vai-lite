package principal

import (
	"net"
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/gateway/auth"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
)

type Kind string

const (
	KindAPIKey Kind = "api_key"
	KindIP     Kind = "ip"
	KindAnon   Kind = "anonymous"
)

type Resolved struct {
	Kind Kind
	// Raw is the raw resolved identifier (API key or IP). It must not be logged.
	Raw string
	// Key is a hashed/bucketed identifier suitable for in-memory maps.
	Key string
}

func Resolve(r *http.Request, cfg config.Config) Resolved {
	if r == nil {
		return Resolved{Kind: KindAnon, Key: "anonymous"}
	}

	if p, ok := auth.PrincipalFrom(r.Context()); ok && p != nil && strings.TrimSpace(p.APIKey) != "" {
		return Resolved{
			Kind: KindAPIKey,
			Raw:  p.APIKey,
			Key:  ratelimit.PrincipalKeyFromAPIKey(p.APIKey),
		}
	}

	ip := resolveClientIP(r, cfg.TrustProxyHeaders)
	if ip == "" {
		return Resolved{Kind: KindAnon, Key: "anonymous"}
	}
	return Resolved{
		Kind: KindIP,
		Raw:  ip,
		Key:  ratelimit.PrincipalKeyFromIP(ip),
	}
}

func resolveClientIP(r *http.Request, trustProxyHeaders bool) string {
	if r == nil {
		return ""
	}

	if trustProxyHeaders {
		if ip := parseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); ip != "" {
			return ip
		}
		if ip := parseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != "" {
			return ip
		}

		if raw := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); raw != "" {
			// XFF can be "client, proxy1, proxy2". Take the left-most.
			first := strings.TrimSpace(strings.Split(raw, ",")[0])
			if ip := parseIP(first); ip != "" {
				return ip
			}
		}
	}

	// Fallback: RemoteAddr.
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return parseIP(host)
}

func parseIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Some proxies include a port; accept "ip:port" as well.
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}

	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return ""
	}
	return ip.String()
}
