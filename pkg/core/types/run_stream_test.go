package types

import "testing"

func TestUnmarshalRunStreamEvent_StreamEventWrapper(t *testing.T) {
	raw := []byte(`{
		"type": "stream_event",
		"event": {
			"type": "content_block_delta",
			"index": 0,
			"delta": {
				"type": "text_delta",
				"text": "hello"
			}
		}
	}`)

	event, err := UnmarshalRunStreamEvent(raw)
	if err != nil {
		t.Fatalf("UnmarshalRunStreamEvent() error = %v", err)
	}

	wrapper, ok := event.(RunStreamEventWrapper)
	if !ok {
		t.Fatalf("event type = %T, want RunStreamEventWrapper", event)
	}

	deltaEvent, ok := wrapper.Event.(ContentBlockDeltaEvent)
	if !ok {
		t.Fatalf("nested event type = %T, want ContentBlockDeltaEvent", wrapper.Event)
	}
	textDelta, ok := deltaEvent.Delta.(TextDelta)
	if !ok {
		t.Fatalf("nested delta type = %T, want TextDelta", deltaEvent.Delta)
	}
	if textDelta.Text != "hello" {
		t.Fatalf("text = %q, want %q", textDelta.Text, "hello")
	}
}

func TestUnmarshalRunStreamEvent_Error(t *testing.T) {
	raw := []byte(`{
		"type": "error",
		"error": {
			"type": "api_error",
			"message": "boom",
			"request_id": "req_123"
		}
	}`)

	event, err := UnmarshalRunStreamEvent(raw)
	if err != nil {
		t.Fatalf("UnmarshalRunStreamEvent() error = %v", err)
	}
	errEvent, ok := event.(RunErrorEvent)
	if !ok {
		t.Fatalf("event type = %T, want RunErrorEvent", event)
	}
	if errEvent.Error.Type != "api_error" {
		t.Fatalf("error.type = %q, want %q", errEvent.Error.Type, "api_error")
	}
	if errEvent.Error.RequestID != "req_123" {
		t.Fatalf("error.request_id = %q, want %q", errEvent.Error.RequestID, "req_123")
	}
}

func TestUnmarshalRunStreamEvent_Unknown(t *testing.T) {
	raw := []byte(`{"type":"future_event","foo":"bar"}`)

	event, err := UnmarshalRunStreamEvent(raw)
	if err != nil {
		t.Fatalf("UnmarshalRunStreamEvent() error = %v", err)
	}
	unknown, ok := event.(UnknownRunStreamEvent)
	if !ok {
		t.Fatalf("event type = %T, want UnknownRunStreamEvent", event)
	}
	if unknown.Type != "future_event" {
		t.Fatalf("unknown.Type = %q, want %q", unknown.Type, "future_event")
	}
	if len(unknown.Raw) == 0 {
		t.Fatalf("unknown.Raw should be preserved")
	}
}
