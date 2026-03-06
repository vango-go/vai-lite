package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
	vai "github.com/vango-go/vai-lite/sdk"
)

func TestRefreshImageStoreFromHistory_DedupesPromotedImage(t *testing.T) {
	t.Parallel()

	image := vai.Image([]byte("hello"), "image/png")
	state := &chatRuntime{
		imageStore: newChatImageStore(),
		history: []vai.Message{
			{
				Role: "user",
				Content: []vai.ContentBlock{
					vai.ToolResult("call_1", []vai.ContentBlock{image}),
				},
			},
			{
				Role:    "assistant",
				Content: []vai.ContentBlock{image},
			},
		},
	}

	newImages := refreshImageStoreFromHistory(state)
	if len(newImages) != 1 {
		t.Fatalf("len(newImages)=%d, want 1", len(newImages))
	}
	if newImages[0].ID != "image_1" {
		t.Fatalf("id=%q, want image_1", newImages[0].ID)
	}

	var out bytes.Buffer
	announceNewImages(&out, newImages)
	if !strings.Contains(out.String(), "[image_1] image/png available. Use /download image_1") {
		t.Fatalf("unexpected announcement: %q", out.String())
	}

	if again := refreshImageStoreFromHistory(state); len(again) != 0 {
		t.Fatalf("len(again)=%d, want 0", len(again))
	}
}

func TestHandleDownloadCommand_SavesBase64Image(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "saved.png")
	state := &chatRuntime{
		imageStore: newChatImageStore(),
		history: []vai.Message{
			{
				Role:    "assistant",
				Content: []vai.ContentBlock{vai.Image([]byte("hello"), "image/png")},
			},
		},
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	handled, err := handleSlashCommand("/download image_1 "+target, state, chatConfig{}, &out, &errOut)
	if err != nil {
		t.Fatalf("handleSlashCommand error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("saved data=%q, want %q", string(data), "hello")
	}
	if !strings.Contains(out.String(), "saved [image_1] to") {
		t.Fatalf("unexpected stdout: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", errOut.String())
	}
}

func TestHandleDownloadCommand_UnknownImage(t *testing.T) {
	t.Parallel()

	state := &chatRuntime{imageStore: newChatImageStore()}
	var out bytes.Buffer
	var errOut bytes.Buffer

	handled, err := handleSlashCommand("/download image_9", state, chatConfig{}, &out, &errOut)
	if err != nil {
		t.Fatalf("handleSlashCommand error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true")
	}
	if !strings.Contains(errOut.String(), `download error: unknown image id "image_9"`) {
		t.Fatalf("unexpected stderr: %q", errOut.String())
	}
}

func TestCompactChatHistoryForModel_StripsImagePayloads(t *testing.T) {
	t.Parallel()

	history := []types.Message{
		{
			Role: "assistant",
			Content: []types.ContentBlock{
				types.ToolUseBlock{Type: "tool_use", ID: "call_1", Name: "vai_image", Input: map[string]any{"prompt": "make an image"}},
			},
		},
		{
			Role: "user",
			Content: []types.ContentBlock{
				types.ToolResultBlock{
					Type:      "tool_result",
					ToolUseID: "call_1",
					Content: []types.ContentBlock{
						types.TextBlock{Type: "text", Text: `{"tool":"vai_image","generated_image_ids":["img-01"]}`},
						types.ImageBlock{Type: "image", Source: types.ImageSource{Type: "base64", MediaType: "image/png", Data: "aGVsbG8="}},
					},
				},
			},
		},
		{
			Role: "assistant",
			Content: []types.ContentBlock{
				types.TextBlock{Type: "text", Text: "created it"},
				types.ImageBlock{Type: "image", Source: types.ImageSource{Type: "base64", MediaType: "image/png", Data: "aGVsbG8="}},
			},
		},
	}

	compacted := compactChatHistoryForModel(history)
	toolResult, ok := compacted[1].ContentBlocks()[0].(types.ToolResultBlock)
	if !ok {
		t.Fatalf("tool result=%T", compacted[1].ContentBlocks()[0])
	}
	if len(toolResult.Content) != 2 {
		t.Fatalf("len(toolResult.Content)=%d, want 2", len(toolResult.Content))
	}
	if !strings.Contains(toolResult.Content[1].(types.TextBlock).Text, "generated image delivered") {
		t.Fatalf("unexpected compacted tool result: %#v", toolResult.Content)
	}
	lastBlocks := compacted[2].ContentBlocks()
	if len(lastBlocks) != 2 {
		t.Fatalf("len(lastBlocks)=%d", len(lastBlocks))
	}
	if tb, ok := lastBlocks[1].(types.TextBlock); !ok || !strings.Contains(tb.Text, "omitted from demo model context") {
		t.Fatalf("assistant image not compacted: %#v", lastBlocks[1])
	}
}
