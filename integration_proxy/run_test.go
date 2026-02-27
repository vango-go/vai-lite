//go:build integration_proxy
// +build integration_proxy

package integration_proxy_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core/types"
	vai "github.com/vango-go/vai-lite/sdk"
)

func TestProxy_MessagesRun_ToolExecution_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 90*time.Second)
		model := providerModel(provider)

		var callCount int64
		weatherTool := vai.MakeTool("get_weather", "Get weather for a location",
			func(ctx context.Context, input struct {
				Location string `json:"location"`
			}) (string, error) {
				atomic.AddInt64(&callCount, 1)
				return "72F and sunny in " + input.Location, nil
			},
		)

		result, err := testClient.Messages.Run(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{
					Role:    "user",
					Content: vai.Text("Use the get_weather tool for location \"San Francisco\" and then answer with one short sentence."),
				},
			},
			ToolChoice: vai.ToolChoiceTool("get_weather"),
			MaxTokens:  256,
		}, vai.WithTools(weatherTool),
			vai.WithMaxToolCalls(1),
			vai.WithMaxTurns(2),
			vai.WithRunTimeout(60*time.Second),
		)
		if err != nil {
			t.Fatalf("Messages.Run error: %v", err)
		}
		if result == nil {
			t.Fatalf("expected non-nil result")
		}
		if atomic.LoadInt64(&callCount) < 1 {
			t.Fatalf("expected tool handler to be called at least once, callCount=%d", atomic.LoadInt64(&callCount))
		}
		if result.ToolCallCount < 1 {
			t.Fatalf("expected at least 1 tool call, got %d", result.ToolCallCount)
		}
		if result.StopReason != vai.RunStopEndTurn && result.StopReason != vai.RunStopMaxToolCalls {
			t.Fatalf("unexpected stop reason: %q", result.StopReason)
		}
		if !runResultToolResultsContainText(result, "sunny") {
			t.Fatalf("expected tool result payload to include %q", "sunny")
		}
	})
}

func runResultToolResultsContainText(result *vai.RunResult, needle string) bool {
	if result == nil {
		return false
	}
	needle = strings.ToLower(needle)
	for _, step := range result.Steps {
		for _, tr := range step.ToolResults {
			for _, block := range tr.Content {
				if tb, ok := block.(types.TextBlock); ok {
					if strings.Contains(strings.ToLower(tb.Text), needle) {
						return true
					}
				}
			}
		}
	}
	return false
}
