package safety

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	MaxURLLength      = 8192
	MaxRedirectHopsV1 = 3
)

var blockedCIDRs = mustParseCIDRs([]string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
})

func ValidateTargetURL(ctx context.Context, rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if len(rawURL) > MaxURLLength {
		return nil, fmt.Errorf("url exceeds maximum length %d", MaxURLLength)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("url credentials are not allowed")
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("url host is required")
	}

	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
		port = ""
	}
	if strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("url host is required")
	}

	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" {
		return nil, fmt.Errorf("invalid hostname")
	}
	if port != "" {
		u.Host = net.JoinHostPort(normalizedHost, port)
	} else {
		u.Host = normalizedHost
	}

	if ip := net.ParseIP(normalizedHost); ip != nil {
		if err := validateIP(ip); err != nil {
			return nil, err
		}
		return u, nil
	}

	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, normalizedHost)
	if err != nil {
		return nil, fmt.Errorf("dns resolution failed: %w", err)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("dns resolution returned no records")
	}
	for _, rec := range resolved {
		if err := validateIP(rec.IP); err != nil {
			return nil, err
		}
	}

	return u, nil
}

func ValidateRedirectTargets(ctx context.Context, targets []*url.URL) error {
	if len(targets) > MaxRedirectHopsV1 {
		return fmt.Errorf("redirect limit exceeded (max %d)", MaxRedirectHopsV1)
	}
	for _, target := range targets {
		if target == nil {
			continue
		}
		if _, err := ValidateTargetURL(ctx, target.String()); err != nil {
			return err
		}
	}
	return nil
}

func validateIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("invalid ip")
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}

	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return fmt.Errorf("destination ip is blocked")
		}
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return fmt.Errorf("destination ip is blocked")
	}
	return nil
}

func mustParseCIDRs(values []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, cidr, err := net.ParseCIDR(value)
		if err != nil {
			panic(err)
		}
		out = append(out, cidr)
	}
	return out
}
