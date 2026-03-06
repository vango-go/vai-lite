package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnmarshalLiveClientFrame(t *testing.T) {
	frame, err := UnmarshalLiveClientFrame([]byte(`{"type":"playback_mark","turn_id":"turn_1","played_ms":250}`))
	if err != nil {
		t.Fatalf("UnmarshalLiveClientFrame error: %v", err)
	}
	mark, ok := frame.(LivePlaybackMarkFrame)
	if !ok {
		t.Fatalf("frame=%T, want LivePlaybackMarkFrame", frame)
	}
	if mark.TurnID != "turn_1" || mark.PlayedMS != 250 {
		t.Fatalf("mark=%#v", mark)
	}

	frame, err = UnmarshalLiveClientFrame([]byte(`{"type":"input_append","content":[{"type":"text","text":"hello"}]}`))
	if err != nil {
		t.Fatalf("UnmarshalLiveClientFrame(input_append) error: %v", err)
	}
	appendFrame, ok := frame.(LiveInputAppendFrame)
	if !ok {
		t.Fatalf("frame=%T, want LiveInputAppendFrame", frame)
	}
	if len(appendFrame.Content) != 1 || appendFrame.Content[0].(TextBlock).Text != "hello" {
		t.Fatalf("appendFrame=%#v", appendFrame)
	}

	frame, err = UnmarshalLiveClientFrame([]byte(`{"type":"input_clear"}`))
	if err != nil {
		t.Fatalf("UnmarshalLiveClientFrame(input_clear) error: %v", err)
	}
	if _, ok := frame.(LiveInputClearFrame); !ok {
		t.Fatalf("frame=%T, want LiveInputClearFrame", frame)
	}
}

func TestUnmarshalLiveClientFrame_RejectsUnknownType(t *testing.T) {
	_, err := UnmarshalLiveClientFrame([]byte(`{"type":"mystery"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown live client frame type") {
		t.Fatalf("expected unknown-type error, got %v", err)
	}
}

func TestUnmarshalLiveServerEvent(t *testing.T) {
	payload, err := json.Marshal(LiveTurnCompleteEvent{
		Type:       "turn_complete",
		TurnID:     "turn_1",
		StopReason: "end_turn",
		History:    []Message{{Role: "assistant", Content: "done"}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	event, err := UnmarshalLiveServerEvent(payload)
	if err != nil {
		t.Fatalf("UnmarshalLiveServerEvent error: %v", err)
	}
	complete, ok := event.(LiveTurnCompleteEvent)
	if !ok {
		t.Fatalf("event=%T, want LiveTurnCompleteEvent", event)
	}
	if complete.TurnID != "turn_1" || len(complete.History) != 1 || complete.History[0].TextContent() != "done" {
		t.Fatalf("complete=%#v", complete)
	}

	event, err = UnmarshalLiveServerEvent([]byte(`{"type":"input_state","content":[{"type":"text","text":"draft"}]}`))
	if err != nil {
		t.Fatalf("UnmarshalLiveServerEvent(input_state) error: %v", err)
	}
	state, ok := event.(LiveInputStateEvent)
	if !ok {
		t.Fatalf("event=%T, want LiveInputStateEvent", event)
	}
	if len(state.Content) != 1 || state.Content[0].(TextBlock).Text != "draft" {
		t.Fatalf("state=%#v", state)
	}
}

func TestUnmarshalLiveServerEvent_RejectsMissingType(t *testing.T) {
	_, err := UnmarshalLiveServerEvent([]byte(`{"message":"oops"}`))
	if err == nil || !strings.Contains(err.Error(), "missing live server event type") {
		t.Fatalf("expected missing-type error, got %v", err)
	}
}
