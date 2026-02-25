package builtins

import (
	"context"
	"testing"

	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/firecrawl"
)

func TestWebFetchBuiltin_Unconfigured(t *testing.T) {
	b := NewWebFetchBuiltin(firecrawl.NewClient("", "", nil))
	_, err := b.Execute(context.Background(), map[string]any{"url": "https://example.com"})
	if err == nil || err.Code != "builtin_not_configured" {
		t.Fatalf("err=%+v", err)
	}
}

func TestWebFetchBuiltin_ValidateURL(t *testing.T) {
	b := NewWebFetchBuiltin(nil)
	_, err := b.Execute(context.Background(), map[string]any{"url": "http://127.0.0.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}
