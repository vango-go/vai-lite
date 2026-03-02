package oai_resp

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/sseframe"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

// eventStream implements EventStream for OpenAI Responses API SSE responses.
type eventStream struct {
	parser      *sseframe.Parser
	closer      io.Closer
	err         error
	responseID  string
	model       string
	accumulator streamAccumulator
	started     bool
	finished    bool
	pending     []types.StreamEvent // Queue for buffered events
}

// streamAccumulator accumulates streamed data.
type streamAccumulator struct {
	textBlockIndex     int
	textContent        strings.Builder
	toolCalls          map[int]*toolCallAccumulator
	blockIndexByOutput map[int]int
	currentItemIndex   int
	finishReason       string
	inputTokens        int
	outputTokens       int
}

// toolCallAccumulator accumulates a single tool call.
type toolCallAccumulator struct {
	ID            string
	Name          string
	ArgumentsJSON strings.Builder
	announced     bool
	blockIndex    int
	outputDone    bool
	argsDone      bool
	stopEmitted   bool
}

// newEventStream creates a new event stream from an HTTP response body.
func newEventStream(body io.ReadCloser) *eventStream {
	return &eventStream{
		parser: sseframe.New(body),
		closer: body,
		accumulator: streamAccumulator{
			toolCalls:          make(map[int]*toolCallAccumulator),
			blockIndexByOutput: make(map[int]int),
		},
	}
}

// Next returns the next event from the stream.
// Returns nil, io.EOF when the stream is complete.
func (s *eventStream) Next() (types.StreamEvent, error) {
	if s.err != nil {
		return nil, s.err
	}

	// Return pending events first
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		return event, nil
	}

	// If finished and no pending events, return EOF
	if s.finished {
		return nil, io.EOF
	}

	for {
		frame, err := s.parser.Next()
		if err != nil {
			if err == io.EOF {
				return s.buildFinalEvent()
			}
			s.err = err
			return nil, err
		}

		data := strings.TrimSpace(string(frame.Data))
		if data == "" {
			continue
		}

		// Check for stream end
		if data == "[DONE]" {
			return s.buildFinalEvent()
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue // Skip unparseable chunks
		}

		// Handle different event types
		result, hasEvent := s.handleStreamEvent(&event)
		if hasEvent {
			return result, nil
		}
	}
}

// handleStreamEvent processes a stream event and returns a Vango event if applicable.
func (s *eventStream) handleStreamEvent(event *streamEvent) (types.StreamEvent, bool) {
	switch event.Type {
	case eventResponseCreated:
		// Response created - emit message_start
		if event.Response != nil {
			s.responseID = event.Response.ID
			s.model = event.Response.Model
		}
		s.started = true
		return types.MessageStartEvent{
			Type: "message_start",
			Message: types.MessageResponse{
				ID:      s.responseID,
				Type:    "message",
				Role:    "assistant",
				Model:   "oai-resp/" + s.model,
				Content: []types.ContentBlock{},
				Usage:   types.Usage{},
			},
		}, true

	case eventOutputItemAdded:
		// New output item added
		if event.Item != nil {
			s.accumulator.currentItemIndex = event.OutputIndex
			return s.handleOutputItemAdded(event.Item, event.OutputIndex)
		}

	case eventOutputTextDelta:
		// Text delta
		if event.Delta != "" {
			s.accumulator.textBlockIndex = s.blockIndexForOutput(event.OutputIndex)
			// If this is the first text content, emit content_block_start AND delta
			if s.accumulator.textContent.Len() == 0 {
				s.accumulator.textContent.WriteString(event.Delta)
				// Queue the delta event for the next call
				s.pending = append(s.pending, types.ContentBlockDeltaEvent{
					Type:  "content_block_delta",
					Index: s.accumulator.textBlockIndex,
					Delta: types.TextDelta{
						Type: "text_delta",
						Text: event.Delta,
					},
				})
				// Return the start event now
				return types.ContentBlockStartEvent{
					Type:         "content_block_start",
					Index:        s.accumulator.textBlockIndex,
					ContentBlock: types.TextBlock{Type: "text", Text: ""},
				}, true
			}

			s.accumulator.textContent.WriteString(event.Delta)
			return types.ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: s.accumulator.textBlockIndex,
				Delta: types.TextDelta{
					Type: "text_delta",
					Text: event.Delta,
				},
			}, true
		}

	case eventFunctionCallArgumentsDelta:
		// Function call arguments delta
		if event.Delta != "" {
			idx := event.OutputIndex
			acc, exists := s.accumulator.toolCalls[idx]
			if !exists {
				acc = &toolCallAccumulator{
					blockIndex: s.blockIndexForOutput(idx),
				}
				s.accumulator.toolCalls[idx] = acc
			}

			acc.ArgumentsJSON.WriteString(event.Delta)
			return types.ContentBlockDeltaEvent{
				Type:  "content_block_delta",
				Index: acc.blockIndex,
				Delta: types.InputJSONDelta{
					Type:        "input_json_delta",
					PartialJSON: event.Delta,
				},
			}, true
		}

	case eventFunctionCallArgumentsDone:
		// Function call arguments completion.
		//
		// Some Responses API streams deliver the final tool-arguments payload in this event (as
		// a full `arguments` string), and may emit output_item.done before it. We must not emit
		// content_block_stop until we've incorporated this tail, or live talk_to_user streaming
		// will truncate captions/TTS.
		payload := event.Arguments
		if payload == "" {
			payload = event.Delta
		}
		if payload != "" {
			idx := event.OutputIndex
			acc, exists := s.accumulator.toolCalls[idx]
			if !exists {
				acc = &toolCallAccumulator{
					blockIndex: s.blockIndexForOutput(idx),
				}
				s.accumulator.toolCalls[idx] = acc
			}
			acc.argsDone = true

			prev := acc.ArgumentsJSON.String()
			suffix := payload
			if prev != "" {
				switch {
				case strings.HasPrefix(payload, prev):
					suffix = payload[len(prev):]
				case strings.HasPrefix(prev, payload):
					suffix = ""
				default:
					// Diverged; replace accumulator with the canonical payload and emit it as a delta
					// so downstream tool parsers can recover.
					acc.ArgumentsJSON.Reset()
				}
			} else {
				acc.ArgumentsJSON.Reset()
			}
			if suffix != "" {
				acc.ArgumentsJSON.WriteString(suffix)
				// If we can, emit the final delta immediately and queue a stop after it.
				if acc.outputDone && !acc.stopEmitted {
					acc.stopEmitted = true
					s.pending = append(s.pending, types.ContentBlockStopEvent{Type: "content_block_stop", Index: acc.blockIndex})
				}
				return types.ContentBlockDeltaEvent{
					Type:  "content_block_delta",
					Index: acc.blockIndex,
					Delta: types.InputJSONDelta{
						Type:        "input_json_delta",
						PartialJSON: suffix,
					},
				}, true
			}
			// No new tail. If output is already done, emit stop now.
			if acc.outputDone && !acc.stopEmitted {
				acc.stopEmitted = true
				return types.ContentBlockStopEvent{Type: "content_block_stop", Index: acc.blockIndex}, true
			}
		}

	case eventOutputTextDone:
		// Text content finished - emit content_block_stop
		if s.accumulator.textContent.Len() > 0 {
			return types.ContentBlockStopEvent{
				Type:  "content_block_stop",
				Index: s.accumulator.textBlockIndex,
			}, true
		}

	case eventOutputItemDone:
		// Output item complete
		if event.Item != nil {
			return s.handleOutputItemDone(event.Item, event.OutputIndex)
		}

	case eventResponseCompleted:
		// Response completed - extract usage
		if event.Response != nil {
			s.accumulator.inputTokens = event.Response.Usage.InputTokens
			s.accumulator.outputTokens = event.Response.Usage.OutputTokens
		}
		s.finished = true
		stopReason := types.StopReasonEndTurn
		if s.accumulator.finishReason == "tool_calls" {
			stopReason = types.StopReasonToolUse
		}

		// If any function_call output items completed but never emitted a stop (for example,
		// if function_call_arguments.done is absent), emit stops now before message_delta.
		var finalEvents []types.StreamEvent
		for _, acc := range s.accumulator.toolCalls {
			if acc == nil {
				continue
			}
			if acc.outputDone && !acc.stopEmitted {
				acc.stopEmitted = true
				finalEvents = append(finalEvents, types.ContentBlockStopEvent{
					Type:  "content_block_stop",
					Index: acc.blockIndex,
				})
			}
		}

		finalEvents = append(finalEvents, types.MessageDeltaEvent{
			Type: "message_delta",
			Delta: struct {
				StopReason types.StopReason `json:"stop_reason,omitempty"`
			}{
				StopReason: stopReason,
			},
			Usage: types.Usage{
				InputTokens:  s.accumulator.inputTokens,
				OutputTokens: s.accumulator.outputTokens,
				TotalTokens:  s.accumulator.inputTokens + s.accumulator.outputTokens,
			},
		})
		finalEvents = append(finalEvents, types.MessageStopEvent{Type: "message_stop"})
		if len(finalEvents) == 0 {
			return nil, false
		}
		// Return first, queue the rest.
		if len(finalEvents) > 1 {
			s.pending = append(s.pending, finalEvents[1:]...)
		}
		return finalEvents[0], true

	case eventResponseFailed:
		// Response failed
		errMsg := "Request failed"
		if event.Response != nil && event.Response.Error != nil {
			errMsg = event.Response.Error.Message
		}
		s.finished = true
		return types.ErrorEvent{
			Type: "error",
			Error: types.Error{
				Type:    "provider_error",
				Message: errMsg,
			},
		}, true
	}

	return nil, false
}

// handleOutputItemAdded handles a new output item.
func (s *eventStream) handleOutputItemAdded(item *outputItem, index int) (types.StreamEvent, bool) {
	switch item.Type {
	case "message":
		// Text message - will get deltas via output_text.delta
		s.accumulator.textBlockIndex = s.blockIndexForOutput(index)
		return nil, false

	case "function_call":
		// Function call started
		blockIndex := s.blockIndexForOutput(index)
		acc, exists := s.accumulator.toolCalls[index]
		if !exists || acc == nil {
			acc = &toolCallAccumulator{}
			s.accumulator.toolCalls[index] = acc
		}
		acc.ID = item.CallID
		acc.Name = item.Name
		acc.blockIndex = blockIndex
		if acc.announced {
			return nil, false
		}
		acc.announced = true
		return types.ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: blockIndex,
			ContentBlock: types.ToolUseBlock{
				Type:  "tool_use",
				ID:    item.CallID,
				Name:  item.Name,
				Input: make(map[string]any),
			},
		}, true
	}

	return nil, false
}

// handleOutputItemDone handles a completed output item.
func (s *eventStream) handleOutputItemDone(item *outputItem, index int) (types.StreamEvent, bool) {
	switch item.Type {
	case "function_call":
		// Mark that we need tool use
		s.accumulator.finishReason = "tool_calls"
		// Note: do not emit content_block_stop here. The final tool args may arrive in
		// response.function_call_arguments.done. We emit stop once args are done (or on response.completed).
		if acc, exists := s.accumulator.toolCalls[index]; exists && acc != nil {
			acc.outputDone = true
			if acc.argsDone && !acc.stopEmitted {
				acc.stopEmitted = true
				return types.ContentBlockStopEvent{Type: "content_block_stop", Index: acc.blockIndex}, true
			}
		}
	}

	return nil, false
}

// blockIndexForOutput returns a stable content block index for a given output item.
func (s *eventStream) blockIndexForOutput(outputIndex int) int {
	if idx, ok := s.accumulator.blockIndexByOutput[outputIndex]; ok {
		return idx
	}
	s.accumulator.blockIndexByOutput[outputIndex] = outputIndex
	return outputIndex
}

// buildFinalEvent builds the final events when stream ends.
func (s *eventStream) buildFinalEvent() (types.StreamEvent, error) {
	s.finished = true

	stopReason := types.StopReasonEndTurn
	if s.accumulator.finishReason == "tool_calls" {
		stopReason = types.StopReasonToolUse
	}

	// Queue message_stop for next call - will return EOF after this
	s.pending = append(s.pending, types.MessageStopEvent{
		Type: "message_stop",
	})

	// Return message_delta with stop reason and usage
	// Return nil error to allow pending events to be delivered
	return types.MessageDeltaEvent{
		Type: "message_delta",
		Delta: struct {
			StopReason types.StopReason `json:"stop_reason,omitempty"`
		}{
			StopReason: stopReason,
		},
		Usage: types.Usage{
			InputTokens:  s.accumulator.inputTokens,
			OutputTokens: s.accumulator.outputTokens,
			TotalTokens:  s.accumulator.inputTokens + s.accumulator.outputTokens,
		},
	}, nil
}

// Close releases resources associated with the stream.
func (s *eventStream) Close() error {
	return s.closer.Close()
}
