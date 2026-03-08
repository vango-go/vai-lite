package chatruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
	s3store "github.com/vango-go/vango-s3"
)

func TestExtractRunResultFromSSE(t *testing.T) {
	event := types.RunCompleteEvent{
		Type: "run_complete",
		Result: &types.RunResult{
			Response: &types.MessageResponse{
				ID:    "msg_123",
				Type:  "message",
				Role:  "assistant",
				Model: "oai-resp/gpt-5-mini",
				Content: []types.ContentBlock{
					types.TextBlock{Type: "text", Text: "hello from stream"},
				},
				Usage: types.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125},
			},
			Usage: types.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125},
		},
	}
	rawEvent, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	raw := []byte("event: run_start\ndata: {\"type\":\"run_start\",\"model\":\"oai-resp/gpt-5-mini\"}\n\n" +
		"event: run_complete\ndata: " + string(rawEvent) + "\n\n")

	result, err := ExtractRunResult(raw)
	if err != nil {
		t.Fatalf("ExtractRunResult() error = %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatal("ExtractRunResult() returned nil result")
	}
	if got := result.Response.TextContent(); got != "hello from stream" {
		t.Fatalf("TextContent() = %q", got)
	}
	if got := result.Usage.TotalTokens; got != 125 {
		t.Fatalf("Usage.TotalTokens = %d", got)
	}
}

func TestExtractGatewayErrorMessage(t *testing.T) {
	raw := []byte(`{"error":{"type":"authentication_error","message":"missing upstream provider api key header","param":"X-Provider-Key-OpenAI"}}`)
	if got := ExtractGatewayErrorMessage(raw); got != "missing upstream provider api key header" {
		t.Fatalf("ExtractGatewayErrorMessage() = %q", got)
	}
}

func TestExtractGatewayErrorMessageSupportsStringEnvelope(t *testing.T) {
	raw := []byte(`{"error":"workspace key missing"}`)
	if got := ExtractGatewayErrorMessage(raw); got != "workspace key missing" {
		t.Fatalf("ExtractGatewayErrorMessage() = %q", got)
	}
}

func TestBuildConversationRunRequestUsesGatewayCompatibleTimeout(t *testing.T) {
	detail := &services.ConversationDetail{
		Conversation: services.Conversation{
			ID:    "conv_123",
			Title: "New chat",
			Model: "oai-resp/gpt-5-mini",
		},
		Messages: []services.ConversationMessage{
			{
				ID:        "msg_1",
				Role:      "user",
				BodyText:  "hi",
				CreatedAt: time.Now(),
			},
			{
				ID:       "msg_2",
				Role:     "assistant",
				BodyText: "hello",
				Attachments: []services.Attachment{
					{
						ID:          "att_1",
						Filename:    "image.png",
						ContentType: "image/png",
						SizeBytes:   128,
						BlobRef:     s3store.BlobRef{Key: "files/t/org/img"},
					},
				},
				CreatedAt: time.Now(),
			},
		},
	}

	headers := http.Header{
		servertools.HeaderProviderKeyTavily:    []string{"tavily-key"},
		servertools.HeaderProviderKeyExa:       []string{"exa-key"},
		servertools.HeaderProviderKeyFirecrawl: []string{"firecrawl-key"},
	}

	req, err := BuildConversationRunRequest(context.Background(), nil, detail, headers)
	if err != nil {
		t.Fatalf("BuildConversationRunRequest() error = %v", err)
	}
	if got := req.Run.TimeoutMS; got != 300000 {
		t.Fatalf("Run.TimeoutMS = %d, want 300000", got)
	}
	if got := req.Run.ToolTimeoutMS; got != 30000 {
		t.Fatalf("Run.ToolTimeoutMS = %d, want 30000", got)
	}
	if len(req.ServerTools) != 2 {
		t.Fatalf("len(ServerTools) = %d, want 2", len(req.ServerTools))
	}
	if got := req.ServerTools[0]; got != servertools.ToolWebSearch {
		t.Fatalf("ServerTools[0] = %q, want %q", got, servertools.ToolWebSearch)
	}
	if got := req.ServerTools[1]; got != servertools.ToolWebFetch {
		t.Fatalf("ServerTools[1] = %q, want %q", got, servertools.ToolWebFetch)
	}
	searchConfig, ok := req.ServerToolConfig[servertools.ToolWebSearch].(map[string]any)
	if !ok {
		t.Fatalf("search tool config has unexpected type %T", req.ServerToolConfig[servertools.ToolWebSearch])
	}
	if got := searchConfig["provider"]; got != servertools.ProviderTavily {
		t.Fatalf("search provider = %#v, want %q", got, servertools.ProviderTavily)
	}
	fetchConfig, ok := req.ServerToolConfig[servertools.ToolWebFetch].(map[string]any)
	if !ok {
		t.Fatalf("fetch tool config has unexpected type %T", req.ServerToolConfig[servertools.ToolWebFetch])
	}
	if got := fetchConfig["provider"]; got != servertools.ProviderFirecrawl {
		t.Fatalf("fetch provider = %#v, want %q", got, servertools.ProviderFirecrawl)
	}
	if got := fetchConfig["format"]; got != "markdown" {
		t.Fatalf("fetch format = %#v, want %q", got, "markdown")
	}
}

func TestConversationServerToolsSkipsUnavailableTools(t *testing.T) {
	tools, config := ConversationServerTools(http.Header{})
	if len(tools) != 0 {
		t.Fatalf("len(tools) = %d, want 0", len(tools))
	}
	if config != nil {
		t.Fatalf("config = %#v, want nil", config)
	}
}

func TestConversationServerToolsFallsBackToAvailableProviders(t *testing.T) {
	headers := http.Header{
		servertools.HeaderProviderKeyExa:    []string{"exa-key"},
		servertools.HeaderProviderKeyTavily: []string{"tavily-key"},
	}

	tools, config := ConversationServerTools(headers)
	if len(tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools))
	}
	if got := tools[0]; got != servertools.ToolWebSearch {
		t.Fatalf("tools[0] = %q, want %q", got, servertools.ToolWebSearch)
	}
	if got := tools[1]; got != servertools.ToolWebFetch {
		t.Fatalf("tools[1] = %q, want %q", got, servertools.ToolWebFetch)
	}
	searchConfig := config[servertools.ToolWebSearch].(map[string]any)
	if got := searchConfig["provider"]; got != servertools.ProviderTavily {
		t.Fatalf("search provider = %#v, want %q", got, servertools.ProviderTavily)
	}
	fetchConfig := config[servertools.ToolWebFetch].(map[string]any)
	if got := fetchConfig["provider"]; got != servertools.ProviderTavily {
		t.Fatalf("fetch provider = %#v, want %q", got, servertools.ProviderTavily)
	}
}

func TestLastUserMessageID(t *testing.T) {
	messages := []services.ConversationMessage{
		{ID: "msg_assistant_1", Role: "assistant"},
		{ID: "msg_user_1", Role: "user"},
		{ID: "msg_assistant_2", Role: "assistant"},
		{ID: "msg_user_2", Role: "user"},
	}
	if got := LastUserMessageID(messages); got != "msg_user_2" {
		t.Fatalf("LastUserMessageID() = %q, want %q", got, "msg_user_2")
	}
}
