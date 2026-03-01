package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

func TestIsConfirmedSpeech(t *testing.T) {
	if IsConfirmedSpeech("...", true, false, true) {
		t.Fatalf("punctuation-only should not confirm speech")
	}
	if !IsConfirmedSpeech("hello", true, false, true) {
		t.Fatalf("expected normal speech to confirm")
	}
	if IsConfirmedSpeech("short", false, true, false) {
		t.Fatalf("assistant-speaking/no-aec should require stricter threshold")
	}
	if !IsConfirmedSpeech("this is a long enough utterance", false, true, false) {
		t.Fatalf("long utterance should confirm speech")
	}
}

func TestIsMeaningfulTranscript(t *testing.T) {
	if isMeaningfulTranscript("hello", "hello") {
		t.Fatalf("same transcript should not be meaningful")
	}
	if !isMeaningfulTranscript("hello world", "hello") {
		t.Fatalf("updated transcript should be meaningful")
	}
}

func TestLiveSession_HandleBackpressure_CancelsAndEnqueuesReset(t *testing.T) {
	s := &LiveSession{
		outboundPriority: make(chan outboundFrame, 1),
		outboundNormal:   make(chan outboundFrame, 1),
	}
	s.canceledAssistant.Store(canceledAssistantState{set: make(map[string]struct{}), order: nil})

	// Fill the priority queue so handleBackpressure must evict to enqueue the latest reset.
	s.outboundPriority <- outboundFrame{textPayload: []byte(`{"type":"audio_reset","reason":"old","assistant_audio_id":"a_old"}`)}

	var ttsCanceled, runCanceled bool
	var ttsCancel context.CancelFunc = func() { ttsCanceled = true }
	var runCancel context.CancelFunc = func() { runCanceled = true }

	err := s.handleBackpressure("a_1", &ttsCancel, &runCancel)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != errBackpressure {
		t.Fatalf("err=%v, want errBackpressure", err)
	}
	if !ttsCanceled || !runCanceled {
		t.Fatalf("expected cancels tts=%v run=%v", ttsCanceled, runCanceled)
	}
	if !s.isAssistantCanceled("a_1") {
		t.Fatalf("expected assistant audio to be marked canceled")
	}

	select {
	case frame := <-s.outboundPriority:
		if !strings.Contains(string(frame.textPayload), `"type":"audio_reset"`) {
			t.Fatalf("expected audio_reset frame, got %q", string(frame.textPayload))
		}
		if !strings.Contains(string(frame.textPayload), `"reason":"backpressure"`) {
			t.Fatalf("expected backpressure reason, got %q", string(frame.textPayload))
		}
		if !strings.Contains(string(frame.textPayload), `"assistant_audio_id":"a_1"`) {
			t.Fatalf("expected assistant_audio_id a_1, got %q", string(frame.textPayload))
		}
	default:
		t.Fatalf("expected a priority frame enqueued")
	}
}

func TestSTTLanguageFromHello_DefaultsToEnWhenMissing(t *testing.T) {
	if got := sttLanguageFromHello(protocol.ClientHello{}); got != "en" {
		t.Fatalf("lang=%q, want en", got)
	}
	if got := sttLanguageFromHello(protocol.ClientHello{Voice: &protocol.HelloVoice{Language: "   "}}); got != "en" {
		t.Fatalf("lang=%q, want en", got)
	}
}

func TestSTTLanguageFromHello_UsesHelloVoiceLanguage(t *testing.T) {
	got := sttLanguageFromHello(protocol.ClientHello{Voice: &protocol.HelloVoice{Language: "es"}})
	if got != "es" {
		t.Fatalf("lang=%q, want es", got)
	}
	got = sttLanguageFromHello(protocol.ClientHello{Voice: &protocol.HelloVoice{Language: "  fr-FR  "}})
	if got != "fr-FR" {
		t.Fatalf("lang=%q, want fr-FR", got)
	}
}

func TestTurnTools_IncludesClientToolsDeterministically(t *testing.T) {
	s := &LiveSession{
		clientTools: map[string]types.Tool{
			"z_tool": {Type: types.ToolTypeFunction, Name: "z_tool", InputSchema: &types.JSONSchema{Type: "object"}},
			"a_tool": {Type: types.ToolTypeFunction, Name: "a_tool", InputSchema: &types.JSONSchema{Type: "object"}},
		},
	}

	tools := s.turnTools()
	if len(tools) != 3 {
		t.Fatalf("len(tools)=%d, want 3", len(tools))
	}
	if tools[0].Name != "talk_to_user" {
		t.Fatalf("tools[0].Name=%q, want talk_to_user", tools[0].Name)
	}
	if tools[1].Name != "a_tool" {
		t.Fatalf("tools[1].Name=%q, want a_tool", tools[1].Name)
	}
	if tools[2].Name != "z_tool" {
		t.Fatalf("tools[2].Name=%q, want z_tool", tools[2].Name)
	}
}

func TestExecuteClientToolCall_TimeoutSendsCancel(t *testing.T) {
	s := &LiveSession{
		outboundNormal:  make(chan outboundFrame, 4),
		toolResultQueue: make(map[string]chan protocol.ClientToolResult),
	}

	toolCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	content, toolErr, execErr := s.executeClientToolCall(toolCtx, context.Background(), 1, types.ToolUseBlock{
		Type:  "tool_use",
		ID:    "call_1",
		Name:  "client_tool",
		Input: map[string]any{"foo": "bar"},
	})
	if execErr != nil {
		t.Fatalf("execErr=%v", execErr)
	}
	if len(content) != 0 {
		t.Fatalf("expected empty content on timeout, got %d blocks", len(content))
	}
	if toolErr == nil || toolErr.Code != "tool_timeout" {
		t.Fatalf("toolErr=%+v, want code=tool_timeout", toolErr)
	}

	// First frame is tool_call, second frame should be tool_cancel.
	first := <-s.outboundNormal
	second := <-s.outboundNormal
	if first.textPayload == nil || second.textPayload == nil {
		t.Fatalf("expected JSON frames to be enqueued")
	}

	var firstMsg map[string]any
	if err := json.Unmarshal(first.textPayload, &firstMsg); err != nil {
		t.Fatalf("decode first frame: %v", err)
	}
	if firstMsg["type"] != "tool_call" {
		t.Fatalf("first frame type=%v, want tool_call", firstMsg["type"])
	}

	var secondMsg map[string]any
	if err := json.Unmarshal(second.textPayload, &secondMsg); err != nil {
		t.Fatalf("decode second frame: %v", err)
	}
	if secondMsg["type"] != "tool_cancel" {
		t.Fatalf("second frame type=%v, want tool_cancel", secondMsg["type"])
	}
}

func TestExecuteClientToolCall_ReturnsResult(t *testing.T) {
	s := &LiveSession{
		outboundNormal:  make(chan outboundFrame, 4),
		toolResultQueue: make(map[string]chan protocol.ClientToolResult),
	}

	done := make(chan struct{})
	var (
		gotContent []types.ContentBlock
		gotToolErr *types.Error
		gotExecErr error
	)
	go func() {
		defer close(done)
		gotContent, gotToolErr, gotExecErr = s.executeClientToolCall(context.Background(), context.Background(), 7, types.ToolUseBlock{
			Type:  "tool_use",
			ID:    "call_77",
			Name:  "client_tool",
			Input: map[string]any{"value": "ping"},
		})
	}()

	frame := <-s.outboundNormal
	var msg map[string]any
	if err := json.Unmarshal(frame.textPayload, &msg); err != nil {
		t.Fatalf("decode tool_call frame: %v", err)
	}
	if msg["type"] != "tool_call" {
		t.Fatalf("frame type=%v, want tool_call", msg["type"])
	}

	dispatched := s.dispatchToolResult(protocol.ClientToolResult{
		Type:   "tool_result",
		TurnID: 7,
		ID:     "call_77",
		Content: []types.ContentBlock{
			types.TextBlock{Type: "text", Text: "ok"},
		},
	})
	if !dispatched {
		t.Fatalf("expected tool_result to be dispatched")
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for executeClientToolCall")
	}

	if gotExecErr != nil {
		t.Fatalf("execErr=%v", gotExecErr)
	}
	if gotToolErr != nil {
		t.Fatalf("toolErr=%+v", gotToolErr)
	}
	if len(gotContent) != 1 {
		t.Fatalf("len(content)=%d, want 1", len(gotContent))
	}
	if txt, ok := gotContent[0].(types.TextBlock); !ok || txt.Text != "ok" {
		t.Fatalf("content[0]=%T %#v", gotContent[0], gotContent[0])
	}
}
