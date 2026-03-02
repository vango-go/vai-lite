package gemini

import "testing"

func TestCapabilities_ToolStreamingIsConservative(t *testing.T) {
	p := New("test-key")
	caps := p.Capabilities()

	if !caps.Tools {
		t.Fatal("expected Tools capability to be true")
	}
	if caps.ToolStreaming {
		t.Fatalf("ToolStreaming = %v, want false", caps.ToolStreaming)
	}
}
