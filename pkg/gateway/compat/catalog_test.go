package compat

import "testing"

func TestProviderKeyHeader_GeminiOAuth(t *testing.T) {
	header, ok := ProviderKeyHeader("gemini-oauth")
	if !ok {
		t.Fatal("expected provider key header for gemini-oauth")
	}
	if header != "X-Provider-Key-Gemini" {
		t.Fatalf("header=%q, want X-Provider-Key-Gemini", header)
	}
}
