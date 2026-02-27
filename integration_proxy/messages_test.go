//go:build integration_proxy
// +build integration_proxy

package integration_proxy_test

import (
	"strings"
	"testing"

	vai "github.com/vango-go/vai-lite/sdk"
)

func TestProxy_MessagesCreate_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := defaultTestContext(t)
		model := providerModel(provider)

		resp, err := testClient.Messages.Create(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{Role: "user", Content: vai.Text("Reply with exactly the single word: pong. Do not include punctuation or extra words.")},
			},
			// Reasoning models can spend small budgets on hidden reasoning.
			// Keep this comfortably above the minimum so we reliably get visible text.
			MaxTokens: 256,
		})
		if err != nil {
			t.Fatalf("Messages.Create error: %v", err)
		}
		if resp == nil || resp.MessageResponse == nil {
			t.Fatalf("expected non-nil response")
		}
		if strings.TrimSpace(resp.Model) == "" {
			t.Fatalf("expected non-empty resp.Model")
		}
		if got := strings.ToLower(resp.TextContent()); !strings.Contains(got, "pong") {
			var blockTypes []string
			for _, b := range resp.Content {
				if b == nil {
					blockTypes = append(blockTypes, "<nil>")
					continue
				}
				blockTypes = append(blockTypes, b.BlockType())
			}
			t.Fatalf(
				"expected response text to include %q, got %q (provider=%q id=%q model=%q stop_reason=%q usage=%+v blockTypes=%v thinking=%q metadata=%v)",
				"pong",
				resp.TextContent(),
				provider.Name,
				resp.ID,
				resp.Model,
				resp.StopReason,
				resp.Usage,
				blockTypes,
				resp.ThinkingContent(),
				resp.Metadata,
			)
		}
	})
}
