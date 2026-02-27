//go:build integration_proxy
// +build integration_proxy

package integration_proxy_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	vai "github.com/vango-go/vai-lite/sdk"
)

func TestProxy_MessagesRunStream_Basic_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 90*time.Second)
		model := providerModel(provider)

		stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{Role: "user", Content: vai.Text("Count from 1 to 3 in one line.")},
			},
			MaxTokens: 256,
		})
		if err != nil {
			t.Fatalf("Messages.RunStream error: %v", err)
		}
		defer stream.Close()

		var (
			sawStepStart    bool
			sawStepComplete bool
			sawRunComplete  bool
			textDeltaCount  int
		)

		for event := range stream.Events() {
			switch e := event.(type) {
			case vai.StepStartEvent:
				sawStepStart = sawStepStart || e.Index >= 0
			case vai.StepCompleteEvent:
				sawStepComplete = sawStepComplete || e.Index >= 0
			case vai.RunCompleteEvent:
				if e.Result != nil {
					sawRunComplete = true
				}
			case vai.StreamEventWrapper:
				if delta, ok := e.Event.(vai.ContentBlockDeltaEvent); ok {
					if textDelta, ok := delta.Delta.(vai.TextDelta); ok && strings.TrimSpace(textDelta.Text) != "" {
						textDeltaCount++
					}
				}
			}
		}

		if streamErr := stream.Err(); streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}

		result := stream.Result()
		if result == nil {
			t.Fatalf("expected non-nil result")
		}
		if result.Response == nil {
			t.Fatalf("expected non-nil result.Response")
		}
		if result.StopReason != vai.RunStopEndTurn && result.StopReason != vai.RunStopMaxTokens {
			t.Fatalf("unexpected stop reason: %q", result.StopReason)
		}
		if !sawStepStart {
			t.Fatalf("expected StepStartEvent")
		}
		if !sawStepComplete {
			t.Fatalf("expected StepCompleteEvent")
		}
		if !sawRunComplete {
			t.Fatalf("expected RunCompleteEvent")
		}
		if textDeltaCount == 0 && strings.TrimSpace(result.Response.TextContent()) == "" {
			if result.Response.Usage.OutputTokens == 0 || result.StopReason == vai.RunStopMaxTokens {
				t.Skipf("%s returned no visible text in stream (provider/model variance)", provider.Name)
			}
			t.Fatalf("expected streamed text deltas or non-empty final text content")
		}
	})
}

func TestProxy_MessagesRunStream_TextDeltas_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 90*time.Second)
		model := providerModel(provider)

		stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{Role: "user", Content: vai.Text("Count from 1 to 5 in one line.")},
			},
			MaxTokens: 256,
		})
		if err != nil {
			t.Fatalf("Messages.RunStream error: %v", err)
		}
		defer stream.Close()

		var textDeltaCount int
		var accumulatedText strings.Builder

		for event := range stream.Events() {
			if wrapper, ok := event.(vai.StreamEventWrapper); ok {
				if delta, ok := wrapper.Event.(vai.ContentBlockDeltaEvent); ok {
					if textDelta, ok := delta.Delta.(vai.TextDelta); ok {
						textDeltaCount++
						accumulatedText.WriteString(textDelta.Text)
					}
				}
			}
		}

		if streamErr := stream.Err(); streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}

		result := stream.Result()
		if result == nil || result.Response == nil {
			t.Fatalf("expected non-nil stream result/response")
		}

		textFromDeltas := accumulatedText.String()
		textFromFinal := result.Response.TextContent()

		if textDeltaCount == 0 && strings.TrimSpace(textFromFinal) == "" {
			t.Fatalf("expected text deltas or non-empty final response text")
		}
		if !strings.Contains(textFromDeltas, "1") && !strings.Contains(textFromFinal, "1") {
			t.Fatalf("expected streamed/final text to contain %q, got deltas=%q final=%q", "1", textFromDeltas, textFromFinal)
		}
	})
}

func TestProxy_MessagesRunStream_HistoryDelta_DefaultHistoryHandlerStrict_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 90*time.Second)
		model := providerModel(provider)

		stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{Role: "user", Content: vai.Text("Say hello.")},
			},
			MaxTokens: 256,
		})
		if err != nil {
			t.Fatalf("Messages.RunStream error: %v", err)
		}
		defer stream.Close()

		history := []vai.Message{
			{Role: "user", Content: vai.Text("Say hello.")},
		}
		applyHistoryDelta := vai.DefaultHistoryHandlerStrict(&history)

		var historyDeltaCount int
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("history handler panicked: %v", r)
				}
			}()
			for event := range stream.Events() {
				if _, ok := event.(vai.HistoryDeltaEvent); ok {
					historyDeltaCount++
				}
				applyHistoryDelta(event)
			}
		}()

		if streamErr := stream.Err(); streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}

		result := stream.Result()
		if result == nil {
			t.Fatalf("expected non-nil stream result")
		}
		if historyDeltaCount == 0 {
			t.Fatalf("expected at least one HistoryDeltaEvent")
		}
		if len(result.Messages) == 0 {
			t.Fatalf("expected non-empty result.Messages")
		}
		if len(history) != len(result.Messages) {
			t.Fatalf("history length=%d, result.Messages length=%d", len(history), len(result.Messages))
		}
	})
}

func TestProxy_MessagesRunStream_BuildTurnMessages_PinsMemoryBlock_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 90*time.Second)
		model := providerModel(provider)

		var buildCalled int64
		var beforeCallCount int64
		var sawEmptyReqMessages atomic.Bool
		var sawMissingMemoryBlock atomic.Bool

		stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{Role: "user", Content: vai.Text("Say hello.")},
			},
			MaxTokens: 256,
		},
			vai.WithBuildTurnMessages(func(info vai.TurnInfo) []vai.Message {
				atomic.AddInt64(&buildCalled, 1)
				out := make([]vai.Message, 0, len(info.History)+1)
				out = append(out, vai.Message{Role: "user", Content: vai.Text("MEMORY: pinned")})
				out = append(out, info.History...)
				return out
			}),
			vai.WithBeforeCall(func(req *vai.MessageRequest) {
				atomic.AddInt64(&beforeCallCount, 1)
				if len(req.Messages) == 0 {
					sawEmptyReqMessages.Store(true)
					return
				}
				if !strings.Contains(req.Messages[0].TextContent(), "MEMORY:") {
					sawMissingMemoryBlock.Store(true)
				}
			}),
		)
		if err != nil {
			t.Fatalf("Messages.RunStream error: %v", err)
		}
		defer stream.Close()

		for range stream.Events() {
		}

		if streamErr := stream.Err(); streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
		if atomic.LoadInt64(&buildCalled) == 0 {
			t.Fatalf("expected WithBuildTurnMessages to be called")
		}
		if atomic.LoadInt64(&beforeCallCount) == 0 {
			t.Fatalf("expected WithBeforeCall to be called")
		}
		if sawEmptyReqMessages.Load() {
			t.Fatalf("expected request messages in WithBeforeCall, got none")
		}
		if sawMissingMemoryBlock.Load() {
			t.Fatalf("expected first request message to contain memory block")
		}
	})
}

func TestProxy_MessagesRunStream_MaxToolCalls_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 120*time.Second)
		model := providerModel(provider)

		pingTool := vai.MakeTool("ping", "Just ping and encourage another ping",
			func(ctx context.Context, input struct{}) (string, error) {
				return "pong received, but you need to ping again", nil
			},
		)

		stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{Role: "user", Content: vai.Text("Keep calling ping 10 times.")},
			},
			ToolChoice: vai.ToolChoiceTool("ping"),
			MaxTokens:  256,
		},
			vai.WithTools(pingTool),
			vai.WithMaxToolCalls(3),
			vai.WithMaxTurns(4),
			vai.WithRunTimeout(60*time.Second),
		)
		if err != nil {
			t.Fatalf("Messages.RunStream error: %v", err)
		}
		defer stream.Close()

		for range stream.Events() {
		}

		if streamErr := stream.Err(); streamErr != nil {
			if isModelToolRefusal(streamErr) {
				t.Skipf("model refused tool usage in RunStream max-tool-calls test: %v", streamErr)
			}
			t.Fatalf("unexpected stream error: %v", streamErr)
		}

		result := stream.Result()
		if result == nil {
			t.Fatalf("expected non-nil result")
		}
		if result.ToolCallCount > 3 {
			t.Fatalf("expected at most 3 tool calls, got %d", result.ToolCallCount)
		}
		if result.StopReason != vai.RunStopMaxToolCalls && result.StopReason != vai.RunStopEndTurn {
			t.Fatalf("expected stop reason max_tool_calls or end_turn, got %q", result.StopReason)
		}
		if result.StopReason == vai.RunStopEndTurn && result.ToolCallCount == 0 {
			t.Log("model ignored tool_choice in streaming mode (provider/model variance)")
		}
	})
}

func TestProxy_MessagesRunStream_Error_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 90*time.Second)
		model := providerModel(provider)

		stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{Role: "user", Content: vai.Text("Say hello.")},
			},
			MaxTokens: 128,
		})
		if err != nil {
			t.Fatalf("Messages.RunStream error: %v", err)
		}
		defer stream.Close()

		for range stream.Events() {
		}

		if streamErr := stream.Err(); streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}

		result := stream.Result()
		if result == nil {
			t.Fatalf("expected non-nil result")
		}
		if result.Response == nil {
			t.Fatalf("expected non-nil result.Response")
		}
		if result.StopReason != vai.RunStopEndTurn && result.StopReason != vai.RunStopMaxTokens {
			t.Fatalf("unexpected stop reason: %q", result.StopReason)
		}
	})
}

func isModelToolRefusal(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "tool_use_failed") || strings.Contains(lower, "did not call a tool")
}

func TestProxy_MessagesRunStream_ToolExecution_OAIResp(t *testing.T) {
	forEachProxyProvider(t, func(t *testing.T, provider proxyProviderConfig) {
		ctx := testContext(t, 120*time.Second)
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

		stream, err := testClient.Messages.RunStream(ctx, &vai.MessageRequest{
			Model: model,
			Messages: []vai.Message{
				{
					Role:    "user",
					Content: vai.Text("Use the get_weather tool for location \"San Francisco\" and then answer with one short sentence."),
				},
			},
			ToolChoice: vai.ToolChoiceTool("get_weather"),
			MaxTokens:  256,
		},
			vai.WithTools(weatherTool),
			vai.WithMaxToolCalls(1),
			vai.WithMaxTurns(2),
			vai.WithRunTimeout(60*time.Second),
		)
		if err != nil {
			t.Fatalf("Messages.RunStream error: %v", err)
		}
		defer stream.Close()

		var (
			toolCallStartCount int
			toolResultCount    int
			sawGetWeatherTool  bool
		)

		for event := range stream.Events() {
			switch e := event.(type) {
			case vai.ToolCallStartEvent:
				toolCallStartCount++
				if e.Name == "get_weather" {
					sawGetWeatherTool = true
				}
			case vai.ToolResultEvent:
				toolResultCount++
			}
		}

		if streamErr := stream.Err(); streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}

		result := stream.Result()
		if result == nil {
			t.Fatalf("expected non-nil result")
		}
		if atomic.LoadInt64(&callCount) < 1 {
			if result.ToolCallCount == 0 && result.StopReason == vai.RunStopEndTurn {
				t.Skipf("%s ignored tool_choice in streaming mode (provider/model variance)", provider.Name)
			}
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
		if result.ToolCallCount > 0 {
			if toolCallStartCount == 0 {
				t.Fatalf("expected ToolCallStartEvent when tool calls occurred")
			}
			if toolResultCount == 0 {
				t.Fatalf("expected ToolResultEvent when tool calls occurred")
			}
			if !sawGetWeatherTool {
				t.Fatalf("expected ToolCallStartEvent for get_weather")
			}
		}
	})
}
