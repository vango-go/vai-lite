package safety

import (
	"context"
	"net/url"
	"testing"
)

func TestValidateTargetURL_RejectsInvalid(t *testing.T) {
	cases := []string{
		"",
		"ftp://example.com",
		"https://user:pass@example.com/",
		"http://127.0.0.1/x",
		"http://169.254.169.254/latest/meta-data",
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
