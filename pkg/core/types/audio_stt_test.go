package types

import "testing"

func TestRequestHasAudioSTT(t *testing.T) {
	req := &MessageRequest{
		Model: "anthropic/test",
		Messages: []Message{
			{
				Role: "user",
				Content: []ContentBlock{
					TextBlock{Type: "text", Text: "hello"},
					AudioSTTBlock{
						Type: "audio_stt",
						Source: AudioSource{
							Type:      "base64",
							MediaType: "audio/wav",
							Data:      "AAAA",
						},
					},
				},
			},
		},
	}
	if !RequestHasAudioSTT(req) {
		t.Fatalf("expected RequestHasAudioSTT to be true")
	}
}

func TestContentBlocksHaveAudioSTT_NestedToolResult(t *testing.T) {
	blocks := []ContentBlock{
		ToolResultBlock{
			Type:      "tool_result",
			ToolUseID: "call_1",
			Content: []ContentBlock{
				AudioSTTBlock{
					Type: "audio_stt",
					Source: AudioSource{
						Type:      "base64",
						MediaType: "audio/wav",
						Data:      "AAAA",
					},
				},
			},
		},
	}
	if !ContentBlocksHaveAudioSTT(blocks) {
		t.Fatalf("expected nested audio_stt block to be detected")
	}
}
