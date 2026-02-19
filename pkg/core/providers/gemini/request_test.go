package gemini

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestBuildRequest_PreservesThoughtSignaturesAcrossRepeatedCalls(t *testing.T) {
	provider := New("test-key")

	req := &types.MessageRequest{
		Model: "gemini/gemini-3-flash-preview",
		Messages: []types.Message{
			{Role: "user", Content: []types.ContentBlock{
				types.TextBlock{Type: "text", Text: "Call get_weather repeatedly"},
			}},
			{Role: "assistant", Content: []types.ContentBlock{
				types.ToolUseBlock{
					Type: "tool_use",
					ID:   "call_get_weather",
					Name: "get_weather",
					Input: map[string]any{
						"location":            "san francisco",
						"__thought_signature": "sig-1",
					},
				},
			}},
			{Role: "user", Content: []types.ContentBlock{
				types.ToolResultBlock{
					Type:      "tool_result",
					ToolUseID: "call_get_weather",
					Content: []types.ContentBlock{
						types.TextBlock{Type: "text", Text: "sunny"},
					},
				},
			}},
		},
	}

	validate := func(gReq *geminiRequest) {
		t.Helper()
		var toolParts []geminiPart
		for _, content := range gReq.Contents {
			for _, part := range content.Parts {
				if part.FunctionCall != nil {
					toolParts = append(toolParts, part)
				}
			}
		}
		if len(toolParts) != 1 {
			t.Fatalf("function call parts = %d, want 1", len(toolParts))
		}
		if toolParts[0].ThoughtSignature != "sig-1" {
			t.Fatalf("thought signature = %q, want %q", toolParts[0].ThoughtSignature, "sig-1")
		}
		if toolParts[0].FunctionCall.Args["location"] != "san francisco" {
			t.Fatalf("function args location = %v, want %q", toolParts[0].FunctionCall.Args["location"], "san francisco")
		}
		if _, ok := toolParts[0].FunctionCall.Args["__thought_signature"]; ok {
			t.Fatalf("__thought_signature leaked into function args: %#v", toolParts[0].FunctionCall.Args)
		}
	}

	first := provider.buildRequest(req)
	validate(first)

	// Rebuilding the request with the same message history must preserve signatures.
	second := provider.buildRequest(req)
	validate(second)

	assistantBlocks := req.Messages[1].ContentBlocks()
	if len(assistantBlocks) != 1 {
		t.Fatalf("assistant blocks = %d, want 1", len(assistantBlocks))
	}
	toolUse, ok := assistantBlocks[0].(types.ToolUseBlock)
	if !ok {
		t.Fatalf("assistant block type = %T, want ToolUseBlock", assistantBlocks[0])
	}
	if got, ok := toolUse.Input["__thought_signature"].(string); !ok || got != "sig-1" {
		t.Fatalf("history thought signature = %v, want %q", toolUse.Input["__thought_signature"], "sig-1")
	}
}
