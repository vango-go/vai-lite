package gemini_oauth

import "testing"

func TestCapabilities_ToolStreamingIsConservative(t *testing.T) {
	p := &Provider{}
	caps := p.Capabilities()

	if !caps.Tools {
		t.Fatal("expected Tools capability to be true")
	}
	if caps.ToolStreaming {
		t.Fatalf("ToolStreaming = %v, want false", caps.ToolStreaming)
	}
}
