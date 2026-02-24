package types

import (
	"encoding/json"
	"fmt"
)

// StreamEvent is the interface for all streaming event types.
type StreamEvent interface {
	EventType() string
}

// Delta is the interface for all delta types in streaming.
type Delta interface {
	DeltaType() string
}

// --- Stream Events (matching API spec section 9) ---

// MessageStartEvent is sent at the beginning of a message.
type MessageStartEvent struct {
	Type    string          `json:"type"` // "message_start"
	Message MessageResponse `json:"message"`
}

func (e MessageStartEvent) EventType() string { return "message_start" }

// ContentBlockStartEvent is sent when a new content block begins.
type ContentBlockStartEvent struct {
	Type         string       `json:"type"` // "content_block_start"
	Index        int          `json:"index"`
	ContentBlock ContentBlock `json:"content_block"`
}

func (e ContentBlockStartEvent) EventType() string { return "content_block_start" }

// ContentBlockDeltaEvent is sent for incremental content updates.
type ContentBlockDeltaEvent struct {
	Type  string `json:"type"` // "content_block_delta"
	Index int    `json:"index"`
	Delta Delta  `json:"delta"`
}

func (e ContentBlockDeltaEvent) EventType() string { return "content_block_delta" }

// ContentBlockStopEvent is sent when a content block is complete.
type ContentBlockStopEvent struct {
	Type  string `json:"type"` // "content_block_stop"
	Index int    `json:"index"`
}

func (e ContentBlockStopEvent) EventType() string { return "content_block_stop" }

// MessageDeltaEvent contains message-level updates (stop_reason, usage).
type MessageDeltaEvent struct {
	Type  string `json:"type"` // "message_delta"
	Delta struct {
		StopReason StopReason `json:"stop_reason,omitempty"`
	} `json:"delta"`
	Usage Usage `json:"usage"`
}

func (e MessageDeltaEvent) EventType() string { return "message_delta" }

// MessageStopEvent is sent when the message is complete.
type MessageStopEvent struct {
	Type string `json:"type"` // "message_stop"
}

func (e MessageStopEvent) EventType() string { return "message_stop" }

// PingEvent is sent periodically to keep the connection alive.
type PingEvent struct {
	Type string `json:"type"` // "ping"
}

func (e PingEvent) EventType() string { return "ping" }

// AudioChunkEvent is an incremental synthesized audio payload emitted by the gateway
// when voice output is enabled for a streaming request.
//
// This event type is gateway-emitted (not provider-emitted) and is intended to be used
// for proxy mode SSE and Live Audio Mode fan-out.
type AudioChunkEvent struct {
	Type         string `json:"type"` // "audio_chunk"
	Format       string `json:"format"`
	Audio        string `json:"audio"`                    // base64-encoded audio bytes
	SampleRateHz int    `json:"sample_rate_hz,omitempty"` // optional (recommended for PCM)
	IsFinal      bool   `json:"is_final,omitempty"`
}

func (e AudioChunkEvent) EventType() string { return "audio_chunk" }

// --- Delta Types ---

// TextDelta contains incremental text content.
type TextDelta struct {
	Type string `json:"type"` // "text_delta"
	Text string `json:"text"`
}

func (d TextDelta) DeltaType() string { return "text_delta" }

// InputJSONDelta contains incremental JSON for tool inputs.
type InputJSONDelta struct {
	Type        string `json:"type"` // "input_json_delta"
	PartialJSON string `json:"partial_json"`
}

func (d InputJSONDelta) DeltaType() string { return "input_json_delta" }

// ThinkingDelta contains incremental thinking content.
type ThinkingDelta struct {
	Type     string `json:"type"` // "thinking_delta"
	Thinking string `json:"thinking"`
}

func (d ThinkingDelta) DeltaType() string { return "thinking_delta" }

// ErrorEvent signals an error during streaming.
type ErrorEvent struct {
	Type  string `json:"type"` // "error"
	Error Error  `json:"error"`
}

func (e ErrorEvent) EventType() string { return "error" }

// Error represents an error in streaming.
type Error struct {
	Type          string `json:"type"`
	Message       string `json:"message"`
	Param         string `json:"param,omitempty"`
	Code          string `json:"code,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	ProviderError any    `json:"provider_error,omitempty"`
	RetryAfter    *int   `json:"retry_after,omitempty"`
}

// UnmarshalStreamEvent deserializes a stream event from JSON.
func UnmarshalStreamEvent(data []byte) (StreamEvent, error) {
	var typeHolder struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeHolder); err != nil {
		return nil, err
	}

	switch typeHolder.Type {
	case "message_start":
		var event MessageStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil

	case "content_block_start":
		// Need to handle ContentBlock deserialization
		var raw struct {
			Type         string          `json:"type"`
			Index        int             `json:"index"`
			ContentBlock json.RawMessage `json:"content_block"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		block, err := UnmarshalContentBlock(raw.ContentBlock)
		if err != nil {
			return nil, err
		}
		return ContentBlockStartEvent{
			Type:         raw.Type,
			Index:        raw.Index,
			ContentBlock: block,
		}, nil

	case "content_block_delta":
		// Need to handle Delta deserialization
		var raw struct {
			Type  string          `json:"type"`
			Index int             `json:"index"`
			Delta json.RawMessage `json:"delta"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		delta, err := UnmarshalDelta(raw.Delta)
		if err != nil {
			return nil, err
		}
		return ContentBlockDeltaEvent{
			Type:  raw.Type,
			Index: raw.Index,
			Delta: delta,
		}, nil

	case "content_block_stop":
		var event ContentBlockStopEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil

	case "message_delta":
		var event MessageDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil

	case "message_stop":
		var event MessageStopEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil

	case "ping":
		var event PingEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil

	case "audio_chunk":
		var event AudioChunkEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil

	case "error":
		var event ErrorEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return event, nil

	default:
		return nil, fmt.Errorf("unknown stream event type: %s", typeHolder.Type)
	}
}

// UnmarshalDelta deserializes a delta from JSON.
func UnmarshalDelta(data []byte) (Delta, error) {
	var typeHolder struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeHolder); err != nil {
		return nil, err
	}

	switch typeHolder.Type {
	case "text_delta":
		var delta TextDelta
		if err := json.Unmarshal(data, &delta); err != nil {
			return nil, err
		}
		return delta, nil

	case "input_json_delta":
		var delta InputJSONDelta
		if err := json.Unmarshal(data, &delta); err != nil {
			return nil, err
		}
		return delta, nil

	case "thinking_delta":
		var delta ThinkingDelta
		if err := json.Unmarshal(data, &delta); err != nil {
			return nil, err
		}
		return delta, nil

	default:
		return nil, fmt.Errorf("unknown delta type: %s", typeHolder.Type)
	}
}
