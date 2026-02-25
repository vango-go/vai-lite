package upstream

import "testing"

func TestFactoryNew_GeminiOAuth(t *testing.T) {
	f := Factory{}
	p, err := f.New("gemini-oauth", "test-key")
	if err != nil {
		t.Fatalf("New returned err=%v", err)
	}
	if p == nil {
		t.Fatal("provider is nil")
	}
}
