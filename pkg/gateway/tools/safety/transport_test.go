package safety

import (
	"io"
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
