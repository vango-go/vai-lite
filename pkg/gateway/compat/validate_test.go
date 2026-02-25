package compat

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestValidateMessageRequest_UnsupportedContentBlock(t *testing.T) {
	req := &types.MessageRequest{
		Model: "openai/gpt-4o",
		Messages: []types.Message{
			{
				Role: "user",
				Content: []types.ContentBlock{
					types.VideoBlock{Type: "video", Source: types.VideoSource{Type: "base64", MediaType: "video/mp4", Data: "Zm9v"}},
				},
			},
		},
	}

	issues := ValidateMessageRequest(req, "openai", req.Model)
	if len(issues) != 1 {
		t.Fatalf("issues len=%d, want 1", len(issues))
	}
	if got := issues[0].Param; got != "messages[0].content[0]" {
		t.Fatalf("param=%q, want messages[0].content[0]", got)
	}
	if got := issues[0].Code; got != "unsupported_content_block" {
		t.Fatalf("code=%q, want unsupported_content_block", got)
	}
	if got := issues[0].Severity; got != "error" {
		t.Fatalf("severity=%q, want error", got)
	}
}

func TestValidateMessageRequest_UnsupportedToolType(t *testing.T) {
	req := &types.MessageRequest{
		Model: "openai/gpt-4o",
		Messages: []types.Message{
			{Role: "user", Content: "hello"},
		},
		Tools: []types.Tool{
			{Type: types.ToolTypeWebSearch},
		},
	}

	issues := ValidateMessageRequest(req, "openai", req.Model)
	if len(issues) != 1 {
		t.Fatalf("issues len=%d, want 1", len(issues))
	}
	if got := issues[0].Param; got != "tools[0].type" {
		t.Fatalf("param=%q, want tools[0].type", got)
	}
	if got := issues[0].Code; got != "unsupported_tool_type" {
		t.Fatalf("code=%q, want unsupported_tool_type", got)
	}
}

func TestValidateMessageRequest_UnsupportedThinking(t *testing.T) {
	req := &types.MessageRequest{
		Model: "groq/llama-3.3-70b-versatile",
		Messages: []types.Message{
			{
				Role: "assistant",
				Content: []types.ContentBlock{
					types.ThinkingBlock{Type: "thinking", Thinking: "internal"},
				},
			},
		},
	}

	issues := ValidateMessageRequest(req, "groq", req.Model)
	if len(issues) != 1 {
		t.Fatalf("issues len=%d, want 1", len(issues))
	}
	if got := issues[0].Code; got != "unsupported_thinking" {
		t.Fatalf("code=%q, want unsupported_thinking", got)
	}
	if got := issues[0].Param; got != "messages[0].content[0]" {
		t.Fatalf("param=%q, want messages[0].content[0]", got)
	}
}

func TestValidateMessageRequest_UnknownProviderPassThrough(t *testing.T) {
	req := &types.MessageRequest{
		Model: "unknown/model",
		Messages: []types.Message{
			{
				Role: "user",
				Content: []types.ContentBlock{
					types.VideoBlock{Type: "video", Source: types.VideoSource{Type: "base64", MediaType: "video/mp4", Data: "Zm9v"}},
				},
			},
		},
		Tools: []types.Tool{
			{Type: types.ToolTypeWebSearch},
		},
		OutputFormat: &types.OutputFormat{
			Type: "json_schema",
			JSONSchema: &types.JSONSchema{
				Type: "object",
			},
		},
	}

	issues := ValidateMessageRequest(req, "unknown", req.Model)
	if len(issues) != 0 {
		t.Fatalf("issues len=%d, want 0", len(issues))
	}
}

func TestValidateMessageRequest_UnknownModelForKnownProviderPassThrough(t *testing.T) {
	req := &types.MessageRequest{
		Model: "openai/not-in-catalog",
		Messages: []types.Message{
			{
				Role: "user",
				Content: []types.ContentBlock{
					types.VideoBlock{Type: "video", Source: types.VideoSource{Type: "base64", MediaType: "video/mp4", Data: "Zm9v"}},
				},
			},
		},
		Tools: []types.Tool{
			{Type: types.ToolTypeWebSearch},
		},
	}

	issues := ValidateMessageRequest(req, "openai", req.Model)
	if len(issues) != 0 {
		t.Fatalf("issues len=%d, want 0", len(issues))
	}
}

func TestValidateMessageRequest_IssuesHaveErrorSeverity(t *testing.T) {
	req := &types.MessageRequest{
		Model: "openai/gpt-4o",
		Messages: []types.Message{
			{
				Role: "assistant",
				Content: []types.ContentBlock{
					types.ThinkingBlock{Type: "thinking", Thinking: "internal"},
					types.DocumentBlock{Type: "document", Source: types.DocumentSource{Type: "base64", MediaType: "application/pdf", Data: "Zm9v"}},
				},
			},
		},
		Tools: []types.Tool{
			{Type: types.ToolTypeWebSearch},
		},
	}

	issues := ValidateMessageRequest(req, "openai", req.Model)
	if len(issues) < 2 {
		t.Fatalf("issues len=%d, want >=2", len(issues))
	}
	for i, issue := range issues {
		if issue.Severity != "error" {
			t.Fatalf("issue[%d].severity=%q, want error", i, issue.Severity)
		}
	}
}
