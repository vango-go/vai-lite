package vai

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestAppendMessageHelpers(t *testing.T) {
	var history []types.Message

	history = AppendUserMessage(history, []types.ContentBlock{Text("hi")})
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].Role != "user" {
		t.Fatalf("history[0].Role = %q, want %q", history[0].Role, "user")
	}

	history = AppendAssistantMessage(history, []types.ContentBlock{Text("hello")})
	if len(history) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(history))
	}
	if history[1].Role != "assistant" {
		t.Fatalf("history[1].Role = %q, want %q", history[1].Role, "assistant")
	}
}

func TestAppendToolResultsMessage(t *testing.T) {
	history := []types.Message{{Role: "user", Content: []types.ContentBlock{Text("start")}}}

	history = AppendToolResultsMessage(history, []ToolExecutionResult{
		{
			ToolUseID: "call_1",
			Content:   []types.ContentBlock{Text("result")},
		},
	})

	if len(history) != 2 {
		t.Fatalf("len(history) = %d, want 2", len(history))
	}
	if history[1].Role != "user" {
		t.Fatalf("history[1].Role = %q, want %q", history[1].Role, "user")
	}

	blocks := history[1].ContentBlocks()
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	trb, ok := blocks[0].(types.ToolResultBlock)
	if !ok {
		t.Fatalf("blocks[0] type = %T, want types.ToolResultBlock", blocks[0])
	}
	if trb.ToolUseID != "call_1" {
		t.Fatalf("ToolUseID = %q, want %q", trb.ToolUseID, "call_1")
	}
	if trb.IsError {
		t.Fatalf("IsError = true, want false")
	}
}
