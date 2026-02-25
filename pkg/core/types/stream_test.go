package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStreamEvent_Types(t *testing.T) {
	tests := []struct {
		event    StreamEvent
		wantType string
	}{
		{MessageStartEvent{Type: "message_start"}, "message_start"},
		{ContentBlockStartEvent{Type: "content_block_start"}, "content_block_start"},
		{ContentBlockDeltaEvent{Type: "content_block_delta"}, "content_block_delta"},
		{ContentBlockStopEvent{Type: "content_block_stop"}, "content_block_stop"},
		{MessageDeltaEvent{Type: "message_delta"}, "message_delta"},
		{MessageStopEvent{Type: "message_stop"}, "message_stop"},
		{PingEvent{Type: "ping"}, "ping"},
		{AudioChunkEvent{Type: "audio_chunk"}, "audio_chunk"},
		{AudioUnavailableEvent{Type: "audio_unavailable"}, "audio_unavailable"},
		{UnknownStreamEvent{Type: "future_event"}, "future_event"},
		{ErrorEvent{Type: "error"}, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.wantType, func(t *testing.T) {
			if got := tt.event.EventType(); got != tt.wantType {
				t.Errorf("EventType() = %q, want %q", got, tt.wantType)
			}
		})
	}
}

func TestDelta_Types(t *testing.T) {
	tests := []struct {
		delta    Delta
		wantType string
	}{
		{TextDelta{Type: "text_delta"}, "text_delta"},
		{InputJSONDelta{Type: "input_json_delta"}, "input_json_delta"},
		{ThinkingDelta{Type: "thinking_delta"}, "thinking_delta"},
		{UnknownDelta{Type: "future_delta"}, "future_delta"},
	}

	for _, tt := range tests {
		t.Run(tt.wantType, func(t *testing.T) {
			if got := tt.delta.DeltaType(); got != tt.wantType {
				t.Errorf("DeltaType() = %q, want %q", got, tt.wantType)
			}
		})
	}
}

func TestUnmarshalStreamEvent_MessageStart(t *testing.T) {
	jsonData := `{
		"type": "message_start",
		"message": {
			"id": "msg_123",
			"type": "message",
			"role": "assistant",
			"content": [],
			"stop_reason": ""
		}
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if event.EventType() != "message_start" {
		t.Errorf("EventType() = %q, want %q", event.EventType(), "message_start")
	}

	ms, ok := event.(MessageStartEvent)
	if !ok {
		t.Fatalf("Expected MessageStartEvent, got %T", event)
	}
	if ms.Message.ID != "msg_123" {
		t.Errorf("Message.ID = %q, want %q", ms.Message.ID, "msg_123")
	}
}

func TestUnmarshalStreamEvent_ContentBlockStart(t *testing.T) {
	jsonData := `{
		"type": "content_block_start",
		"index": 0,
		"content_block": {
			"type": "text",
			"text": ""
		}
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cbs, ok := event.(ContentBlockStartEvent)
	if !ok {
		t.Fatalf("Expected ContentBlockStartEvent, got %T", event)
	}
	if cbs.Index != 0 {
		t.Errorf("Index = %d, want 0", cbs.Index)
	}
	if cbs.ContentBlock.BlockType() != "text" {
		t.Errorf("ContentBlock type = %q, want %q", cbs.ContentBlock.BlockType(), "text")
	}
}

func TestUnmarshalStreamEvent_ContentBlockDelta(t *testing.T) {
	jsonData := `{
		"type": "content_block_delta",
		"index": 0,
		"delta": {
			"type": "text_delta",
			"text": "Hello"
		}
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cbd, ok := event.(ContentBlockDeltaEvent)
	if !ok {
		t.Fatalf("Expected ContentBlockDeltaEvent, got %T", event)
	}
	if cbd.Index != 0 {
		t.Errorf("Index = %d, want 0", cbd.Index)
	}

	textDelta, ok := cbd.Delta.(TextDelta)
	if !ok {
		t.Fatalf("Expected TextDelta, got %T", cbd.Delta)
	}
	if textDelta.Text != "Hello" {
		t.Errorf("Text = %q, want %q", textDelta.Text, "Hello")
	}
}

func TestUnmarshalStreamEvent_MessageDelta(t *testing.T) {
	jsonData := `{
		"type": "message_delta",
		"delta": {
			"stop_reason": "end_turn"
		},
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"total_tokens": 150
		}
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	md, ok := event.(MessageDeltaEvent)
	if !ok {
		t.Fatalf("Expected MessageDeltaEvent, got %T", event)
	}
	if md.Delta.StopReason != StopReasonEndTurn {
		t.Errorf("StopReason = %q, want %q", md.Delta.StopReason, StopReasonEndTurn)
	}
	if md.Usage.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", md.Usage.TotalTokens)
	}
}

func TestUnmarshalStreamEvent_AudioChunk(t *testing.T) {
	jsonData := `{
		"type": "audio_chunk",
		"format": "pcm_s16le",
		"audio": "AQID",
		"sample_rate_hz": 24000,
		"is_final": false
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	ac, ok := event.(AudioChunkEvent)
	if !ok {
		t.Fatalf("Expected AudioChunkEvent, got %T", event)
	}
	if ac.Format != "pcm_s16le" {
		t.Errorf("Format = %q, want %q", ac.Format, "pcm_s16le")
	}
	if ac.Audio != "AQID" {
		t.Errorf("Audio = %q, want %q", ac.Audio, "AQID")
	}
	if ac.SampleRateHz != 24000 {
		t.Errorf("SampleRateHz = %d, want %d", ac.SampleRateHz, 24000)
	}
	if ac.IsFinal {
		t.Errorf("IsFinal = true, want false")
	}
}

func TestUnmarshalStreamEvent_AudioUnavailable(t *testing.T) {
	jsonData := `{
		"type": "audio_unavailable",
		"reason": "tts_failed",
		"message": "TTS synthesis failed: connection reset"
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	au, ok := event.(AudioUnavailableEvent)
	if !ok {
		t.Fatalf("Expected AudioUnavailableEvent, got %T", event)
	}
	if au.Reason != "tts_failed" {
		t.Errorf("Reason = %q, want %q", au.Reason, "tts_failed")
	}
	if au.Message != "TTS synthesis failed: connection reset" {
		t.Errorf("Message = %q, want %q", au.Message, "TTS synthesis failed: connection reset")
	}
}

func TestUnmarshalStreamEvent_UnknownEventType_IsOpaque(t *testing.T) {
	jsonData := `{
		"type":"future_event",
		"foo":"bar",
		"nested":{"a":1}
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	unknown, ok := event.(UnknownStreamEvent)
	if !ok {
		t.Fatalf("Expected UnknownStreamEvent, got %T", event)
	}
	if unknown.EventType() != "future_event" {
		t.Fatalf("EventType()=%q, want future_event", unknown.EventType())
	}
	if len(unknown.Raw) == 0 {
		t.Fatalf("Raw is empty for unknown event")
	}
}

func TestUnmarshalStreamEvent_ContentBlockDelta_WithUnknownDelta(t *testing.T) {
	jsonData := `{
		"type":"content_block_delta",
		"index":2,
		"delta":{"type":"future_delta","x":1}
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	cbd, ok := event.(ContentBlockDeltaEvent)
	if !ok {
		t.Fatalf("Expected ContentBlockDeltaEvent, got %T", event)
	}
	unknown, ok := cbd.Delta.(UnknownDelta)
	if !ok {
		t.Fatalf("Expected UnknownDelta, got %T", cbd.Delta)
	}
	if unknown.DeltaType() != "future_delta" {
		t.Fatalf("DeltaType()=%q, want future_delta", unknown.DeltaType())
	}
}

func TestUnmarshalStreamEvent_ErrorWithExtendedFields(t *testing.T) {
	jsonData := `{
		"type":"error",
		"error":{
			"type":"rate_limit_error",
			"message":"too many requests",
			"param":"model",
			"code":"rl",
			"request_id":"req_123",
			"retry_after":10,
			"provider_error":{"raw":"x"},
			"compat_issues":[
				{"severity":"error","param":"messages[0].content[0]","code":"unsupported_content_block","message":"video blocks are not supported"}
			]
		}
	}`

	event, err := UnmarshalStreamEvent([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	ee, ok := event.(ErrorEvent)
	if !ok {
		t.Fatalf("Expected ErrorEvent, got %T", event)
	}
	if ee.Error.Type != "rate_limit_error" {
		t.Errorf("Error.Type = %q, want %q", ee.Error.Type, "rate_limit_error")
	}
	if ee.Error.Param != "model" {
		t.Errorf("Error.Param = %q, want %q", ee.Error.Param, "model")
	}
	if ee.Error.Code != "rl" {
		t.Errorf("Error.Code = %q, want %q", ee.Error.Code, "rl")
	}
	if ee.Error.RequestID != "req_123" {
		t.Errorf("Error.RequestID = %q, want %q", ee.Error.RequestID, "req_123")
	}
	if ee.Error.RetryAfter == nil || *ee.Error.RetryAfter != 10 {
		if ee.Error.RetryAfter == nil {
			t.Fatalf("Error.RetryAfter is nil, want 10")
		}
		t.Errorf("Error.RetryAfter = %d, want %d", *ee.Error.RetryAfter, 10)
	}
	if ee.Error.ProviderError == nil {
		t.Fatalf("Error.ProviderError is nil, want object")
	}
	if len(ee.Error.CompatIssues) != 1 {
		t.Fatalf("Error.CompatIssues len = %d, want 1", len(ee.Error.CompatIssues))
	}
	if ee.Error.CompatIssues[0].Code != "unsupported_content_block" {
		t.Fatalf("Error.CompatIssues[0].Code = %q, want unsupported_content_block", ee.Error.CompatIssues[0].Code)
	}
}

func TestUnmarshalDelta(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantType string
	}{
		{
			name:     "text_delta",
			json:     `{"type":"text_delta","text":"Hello"}`,
			wantType: "text_delta",
		},
		{
			name:     "input_json_delta",
			json:     `{"type":"input_json_delta","partial_json":"{\"foo\":"}`,
			wantType: "input_json_delta",
		},
		{
			name:     "thinking_delta",
			json:     `{"type":"thinking_delta","thinking":"Let me think..."}`,
			wantType: "thinking_delta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, err := UnmarshalDelta([]byte(tt.json))
			if err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}
			if delta.DeltaType() != tt.wantType {
				t.Errorf("DeltaType() = %q, want %q", delta.DeltaType(), tt.wantType)
			}
		})
	}
}

func TestUnmarshalDelta_UnknownDeltaType_IsOpaque(t *testing.T) {
	jsonData := `{"type":"future_delta","x":1,"extra":{"k":"v"}}`

	delta, err := UnmarshalDelta([]byte(jsonData))
	if err != nil {
		t.Fatalf("Failed to unmarshal unknown delta: %v", err)
	}

	unknown, ok := delta.(UnknownDelta)
	if !ok {
		t.Fatalf("Expected UnknownDelta, got %T", delta)
	}
	if unknown.DeltaType() != "future_delta" {
		t.Fatalf("DeltaType()=%q, want future_delta", unknown.DeltaType())
	}
	if len(unknown.Raw) == 0 {
		t.Fatalf("Raw is empty for unknown delta")
	}
}

func TestUnknownOpaqueRoundTripPreservesPayload(t *testing.T) {
	t.Run("unknown_event", func(t *testing.T) {
		original := []byte(`{"type":"future_event","foo":"bar","nested":{"a":1}}`)
		event, err := UnmarshalStreamEvent(original)
		if err != nil {
			t.Fatalf("UnmarshalStreamEvent() error = %v", err)
		}
		unknown, ok := event.(UnknownStreamEvent)
		if !ok {
			t.Fatalf("Expected UnknownStreamEvent, got %T", event)
		}

		marshaled, err := json.Marshal(unknown)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		assertJSONSemanticEqual(t, original, marshaled)
	})

	t.Run("unknown_delta", func(t *testing.T) {
		original := []byte(`{"type":"future_delta","x":1,"arr":[1,2,3]}`)
		delta, err := UnmarshalDelta(original)
		if err != nil {
			t.Fatalf("UnmarshalDelta() error = %v", err)
		}
		unknown, ok := delta.(UnknownDelta)
		if !ok {
			t.Fatalf("Expected UnknownDelta, got %T", delta)
		}

		marshaled, err := json.Marshal(unknown)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		assertJSONSemanticEqual(t, original, marshaled)
	})

	t.Run("content_block_delta_with_unknown_delta", func(t *testing.T) {
		original := []byte(`{"type":"content_block_delta","index":3,"delta":{"type":"future_delta","x":1,"y":{"z":2}}}`)
		event, err := UnmarshalStreamEvent(original)
		if err != nil {
			t.Fatalf("UnmarshalStreamEvent() error = %v", err)
		}

		marshaled, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		assertJSONSemanticEqual(t, original, marshaled)
	})
}

func TestTextDelta_MarshalJSON(t *testing.T) {
	delta := TextDelta{Type: "text_delta", Text: "Hello, world!"}
	data, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var unmarshaled TextDelta
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if unmarshaled.Text != "Hello, world!" {
		t.Errorf("Text = %q, want %q", unmarshaled.Text, "Hello, world!")
	}
}

func assertJSONSemanticEqual(t *testing.T, left, right []byte) {
	t.Helper()

	var leftObj any
	if err := json.Unmarshal(left, &leftObj); err != nil {
		t.Fatalf("left JSON unmarshal error: %v", err)
	}

	var rightObj any
	if err := json.Unmarshal(right, &rightObj); err != nil {
		t.Fatalf("right JSON unmarshal error: %v", err)
	}

	if !reflect.DeepEqual(leftObj, rightObj) {
		t.Fatalf("JSON mismatch:\nleft:  %s\nright: %s", string(left), string(right))
	}
}
