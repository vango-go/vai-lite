package main

import "testing"

func TestBuildWorkOSClientHonorsJWTEnv(t *testing.T) {
	t.Setenv("WORKOS_API_KEY", "sk_test_1234567890")
	t.Setenv("WORKOS_CLIENT_ID", "client_1234567890")
	t.Setenv("WORKOS_COOKIE_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("WORKOS_JWT_ISSUER", "https://auth.example.com")
	t.Setenv("WORKOS_JWT_AUDIENCE", "custom_audience")

	client, _, err := buildWorkOSClient("http://localhost:8000")
	if err != nil {
		t.Fatalf("buildWorkOSClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("buildWorkOSClient() returned nil client")
	}

	cfg := client.Config()
	if cfg.JWTIssuer != "https://auth.example.com" {
		t.Fatalf("JWTIssuer = %q, want %q", cfg.JWTIssuer, "https://auth.example.com")
	}
	if cfg.JWTAudience != "custom_audience" {
		t.Fatalf("JWTAudience = %q, want %q", cfg.JWTAudience, "custom_audience")
	}
	if cfg.RedirectURI != "http://localhost:8000/auth/callback" {
		t.Fatalf("RedirectURI = %q, want %q", cfg.RedirectURI, "http://localhost:8000/auth/callback")
	}
}
