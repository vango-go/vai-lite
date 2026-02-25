package safety

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestReadResponseBodyLimited(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("abcdef"))}
	if _, err := ReadResponseBodyLimited(resp, 3); err == nil {
		t.Fatal("expected limit error")
	}
}

func TestDecodeJSONBodyLimited_ContentType(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/plain"}},
		Body:   io.NopCloser(strings.NewReader("{}")),
	}
	var out map[string]any
	if err := DecodeJSONBodyLimited(resp, 1024, &out); err == nil {
		t.Fatal("expected content-type error")
	}
}

func TestDecodeJSONBodyLimited_Malformed(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader("{")),
	}
	var out map[string]any
	if err := DecodeJSONBodyLimited(resp, 1024, &out); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestDecodeJSONBodyLimited_TrailingPayload(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"ok":true}{"bad":true}`)),
	}
	var out map[string]any
	if err := DecodeJSONBodyLimited(resp, 1024, &out); err == nil {
		t.Fatal("expected trailing payload error")
	}
}

func TestNewRestrictedHTTPClient_RedirectChecks(t *testing.T) {
	c := NewRestrictedHTTPClient(nil)
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}}
	via := []*http.Request{
		{URL: &url.URL{Scheme: "https", Host: "example.com"}},
		{URL: &url.URL{Scheme: "https", Host: "example.com"}},
		{URL: &url.URL{Scheme: "https", Host: "example.com"}},
		{URL: &url.URL{Scheme: "https", Host: "example.com"}},
	}
	if err := c.CheckRedirect(req, via); err == nil {
		t.Fatal("expected redirect hop limit error")
	}

	reqPrivate := &http.Request{URL: &url.URL{Scheme: "http", Host: "127.0.0.1"}}
	if err := c.CheckRedirect(reqPrivate, nil); err == nil {
		t.Fatal("expected private host redirect error")
	}
}

func TestNewRestrictedHTTPClient_DisablesProxy(t *testing.T) {
	base := &http.Client{
		Transport: &http.Transport{
			Proxy:              http.ProxyFromEnvironment,
			ProxyConnectHeader: http.Header{"X-Test": []string{"1"}},
			GetProxyConnectHeader: func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error) {
				return http.Header{"X-Test": []string{"1"}}, nil
			},
		},
	}
	c := NewRestrictedHTTPClient(base)
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport=%T, want *http.Transport", c.Transport)
	}
	if tr.Proxy != nil {
		t.Fatalf("proxy should be nil on restricted transport")
	}
	if tr.ProxyConnectHeader != nil {
		t.Fatalf("proxy connect headers should be nil on restricted transport")
	}
	if tr.GetProxyConnectHeader != nil {
		t.Fatalf("get proxy connect header callback should be nil on restricted transport")
	}
}

func TestValidateDialTarget_IPv6LiteralAllowed(t *testing.T) {
	ip, err := validateDialTarget(context.Background(), "2001:db8::1")
	if err != nil {
		t.Fatalf("validateDialTarget err=%v", err)
	}
	if !ip.Equal(net.ParseIP("2001:db8::1")) {
		t.Fatalf("ip=%v", ip)
	}
}

func TestValidateDialTarget_RejectsBlockedIPv6(t *testing.T) {
	if _, err := validateDialTarget(context.Background(), "fe80::1"); err == nil {
		t.Fatal("expected blocked ipv6 error")
	}
}
