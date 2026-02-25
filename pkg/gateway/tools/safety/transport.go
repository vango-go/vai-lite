package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MaxDownloadedBytesV1 int64 = 5 << 20

// NewRestrictedHTTPClient returns an HTTP client with redirect limits and DNS/IP validation.
// This is intended for outbound tool HTTP requests.
func NewRestrictedHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	}

	out := *base
	if out.Transport == nil {
		out.Transport = http.DefaultTransport
	}

	if tr, ok := out.Transport.(*http.Transport); ok {
		clone := tr.Clone()
		clone.Proxy = nil
		clone.ProxyConnectHeader = nil
		clone.GetProxyConnectHeader = nil
		clone.DialTLSContext = nil
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		clone.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if _, err := strconv.Atoi(port); err != nil {
				return nil, fmt.Errorf("invalid port")
			}
			selected, err := validateDialTarget(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(selected.String(), port))
		}
		out.Transport = clone
	}

	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > MaxRedirectHopsV1 {
			return fmt.Errorf("redirect limit exceeded (max %d)", MaxRedirectHopsV1)
		}
		if req != nil && req.URL != nil {
			if _, err := ValidateTargetURL(req.Context(), req.URL.String()); err != nil {
				return err
			}
		}
		return nil
	}

	return &out
}

func validateDialTarget(ctx context.Context, host string) (net.IP, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("invalid host")
	}
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("invalid host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := validateIP(ip); err != nil {
			return nil, err
		}
		return ip, nil
	}
	if !isASCII(host) {
		return nil, fmt.Errorf("invalid host")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("dns resolution returned no records")
	}
	for _, rec := range ips {
		if err := validateIP(rec.IP); err != nil {
			return nil, err
		}
	}
	for _, rec := range ips {
		if rec.IP != nil {
			return rec.IP, nil
		}
	}
	return nil, fmt.Errorf("dns resolution returned no records")
}

func ReadResponseBodyLimited(resp *http.Response, limit int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("response body is empty")
	}
	if limit <= 0 {
		limit = MaxDownloadedBytesV1
	}
	lr := &io.LimitedReader{R: resp.Body, N: limit + 1}
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response exceeds maximum size %d bytes", limit)
	}
	return b, nil
}

func DecodeJSONBodyLimited(resp *http.Response, limit int64, out any) error {
	b, err := ReadResponseBodyLimited(resp, limit)
	if err != nil {
		return err
	}
	if ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type"))); ct != "" && !strings.Contains(ct, "application/json") {
		return fmt.Errorf("unexpected content type %q", ct)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("invalid json payload")
	}
	return nil
}
