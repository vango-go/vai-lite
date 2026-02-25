package safety

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestValidateTargetURL_RejectsInvalid(t *testing.T) {
	cases := []string{
		"",
		"ftp://example.com",
		"https://user:pass@example.com/",
		"http://127.0.0.1/x",
		"http://[::1]/x",
		"http://[::ffff:127.0.0.1]/x",
		"http://[fe80::1]/x",
		"http://169.254.169.254/latest/meta-data",
		"http://%31%32%37.0.0.1/x",
		"https://example.com:99999",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			if _, err := ValidateTargetURL(context.Background(), tc); err == nil {
				t.Fatalf("expected error for %q", tc)
			}
		})
	}
}

func TestValidateTargetURL_AllowsPublicHTTP(t *testing.T) {
	u, err := ValidateTargetURL(context.Background(), "https://1.1.1.1/path")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got := u.Scheme; got != "https" {
		t.Fatalf("scheme=%q", got)
	}
}

func TestValidateRedirectTargets_Limit(t *testing.T) {
	targets := []*url.URL{
		{Scheme: "https", Host: "example.com"},
		{Scheme: "https", Host: "example.com"},
		{Scheme: "https", Host: "example.com"},
		{Scheme: "https", Host: "example.com"},
	}
	if err := ValidateRedirectTargets(context.Background(), targets); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateTargetURL_LengthLimit(t *testing.T) {
	tooLong := "https://example.com/" + strings.Repeat("a", MaxURLLength)
	if _, err := ValidateTargetURL(context.Background(), tooLong); err == nil {
		t.Fatal("expected length error")
	}
}

func TestValidateTargetURL_RejectsNonASCIIHost(t *testing.T) {
	if _, err := ValidateTargetURL(context.Background(), "https://münich.example/path"); err == nil {
		t.Fatal("expected non-ascii host rejection")
	}
}

func TestValidateRedirectTargets_RevalidatesDestination(t *testing.T) {
	allowed := &url.URL{Scheme: "https", Host: "1.1.1.1"}
	blocked := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", "80")}
	if err := ValidateRedirectTargets(context.Background(), []*url.URL{allowed, blocked}); err == nil {
		t.Fatal("expected blocked redirect target")
	}
}
