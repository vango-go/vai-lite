package types

import "testing"

func TestUnmarshalChainStartPayloadStrict_AcceptsMultiSegmentModelID(t *testing.T) {
	payload, err := UnmarshalChainStartPayloadStrict([]byte(`{
		"defaults":{"model":"openrouter/anthropic/claude-haiku-4.5"},
		"history":[{"role":"user","content":"hello"}]
	}`))
	if err != nil {
		t.Fatalf("UnmarshalChainStartPayloadStrict() err = %v", err)
	}
	if got := payload.Defaults.Model; got != "openrouter/anthropic/claude-haiku-4.5" {
		t.Fatalf("defaults.model = %q", got)
	}
}
