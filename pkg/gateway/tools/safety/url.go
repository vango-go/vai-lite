package safety

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
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
	decodedURL, err := decodeURLForValidation(rawURL)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(decodedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("url credentials are not allowed")
	}
	host := strings.TrimSpace(u.Hostname())
	port := strings.TrimSpace(u.Port())
	if host == "" {
		return nil, fmt.Errorf("url host is required")
	}
	if !isASCII(host) {
		// Conservative policy for SSRF safety: reject non-ASCII hosts unless
		// an explicit IDNA normalization layer is introduced.
		return nil, fmt.Errorf("url host must be ascii")
	}
	if port != "" {
		if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("invalid port")
		}
	}

	normalizedHost := strings.ToLower(host)
	if normalizedHost == "" {
		return nil, fmt.Errorf("invalid hostname")
	}
	if strings.Contains(normalizedHost, "%") {
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
	if isIPv4MappedIPv6(ip) {
		ip = ip.To4()
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

func decodeURLForValidation(rawURL string) (string, error) {
	decoded := rawURL
	for i := 0; i < 3; i++ {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", fmt.Errorf("invalid percent-encoding in url")
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	return decoded, nil
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func isIPv4MappedIPv6(ip net.IP) bool {
	return len(ip) == net.IPv6len && bytes.Equal(ip[:12], []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff})
}
