package runloop

import (
	"encoding/json"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

// StreamAccumulator reconstructs a MessageResponse from stream events.
type StreamAccumulator struct {
	response         types.MessageResponse
	currentContent   []types.ContentBlock
	toolInputBuffers map[int]string
}

func NewStreamAccumulator() *StreamAccumulator {
	return &StreamAccumulator{
		toolInputBuffers: make(map[int]string),
	}
}

func (a *StreamAccumulator) Apply(event types.StreamEvent) {
	switch e := event.(type) {
	case types.MessageStartEvent:
		a.response = e.Message
	case *types.MessageStartEvent:
		if e != nil {
			a.response = e.Message
		}
	case types.ContentBlockStartEvent:
		a.ensureContentIndex(e.Index)
		a.currentContent[e.Index] = e.ContentBlock
	case *types.ContentBlockStartEvent:
		if e != nil {
			a.ensureContentIndex(e.Index)
			a.currentContent[e.Index] = e.ContentBlock
		}
	case types.ContentBlockDeltaEvent:
		a.applyDelta(e.Index, e.Delta)
	case *types.ContentBlockDeltaEvent:
		if e != nil {
			a.applyDelta(e.Index, e.Delta)
		}
	case types.ContentBlockStopEvent:
		a.applyToolInput(e.Index)
	case *types.ContentBlockStopEvent:
		if e != nil {
			a.applyToolInput(e.Index)
		}
	case types.MessageDeltaEvent:
		a.response.StopReason = e.Delta.StopReason
		a.response.Usage = e.Usage
	case *types.MessageDeltaEvent:
		if e != nil {
			a.response.StopReason = e.Delta.StopReason
			a.response.Usage = e.Usage
		}
	}
}

func (a *StreamAccumulator) Response() *types.MessageResponse {
	for idx := range a.toolInputBuffers {
		a.applyToolInput(idx)
	}
	resp := a.response
	resp.Content = a.currentContent
	return &resp
}

func (a *StreamAccumulator) ensureContentIndex(index int) {
	for len(a.currentContent) <= index {
		a.currentContent = append(a.currentContent, nil)
	}
}

func (a *StreamAccumulator) applyDelta(index int, delta types.Delta) {
	if index < 0 {
		return
	}
	a.ensureContentIndex(index)

	if inputDelta, ok := delta.(types.InputJSONDelta); ok {
		a.toolInputBuffers[index] += inputDelta.PartialJSON
	}

	switch d := delta.(type) {
	case types.TextDelta:
		switch block := a.currentContent[index].(type) {
		case types.TextBlock:
			block.Text += d.Text
			a.currentContent[index] = block
		case *types.TextBlock:
			if block != nil {
				block.Text += d.Text
			}
		}
	case types.ThinkingDelta:
		switch block := a.currentContent[index].(type) {
		case types.ThinkingBlock:
			block.Thinking += d.Thinking
			a.currentContent[index] = block
		case *types.ThinkingBlock:
			if block != nil {
				block.Thinking += d.Thinking
			}
		}
	}
}

func (a *StreamAccumulator) applyToolInput(index int) {
	raw, ok := a.toolInputBuffers[index]
	if !ok {
		return
	}
	delete(a.toolInputBuffers, index)
	if raw == "" || index < 0 || index >= len(a.currentContent) {
		return
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return
	}

	switch block := a.currentContent[index].(type) {
	case types.ToolUseBlock:
		block.Input = parsed
		a.currentContent[index] = block
	case *types.ToolUseBlock:
		if block != nil {
			block.Input = parsed
		}
	case types.ServerToolUseBlock:
		block.Input = parsed
		a.currentContent[index] = block
	case *types.ServerToolUseBlock:
		if block != nil {
			block.Input = parsed
		}
	}
}
