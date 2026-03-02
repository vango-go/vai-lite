package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/sseframe"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

// eventStream implements EventStream for Gemini SSE responses.
type eventStream struct {
	parser      *sseframe.Parser
	closer      io.Closer
	model       string
	err         error
	accumulator streamAccumulator
	started     bool
	finished    bool
	pending     []types.StreamEvent // Queue for buffered events
}

// streamAccumulator accumulates streamed data.
type streamAccumulator struct {
	textIndex    int
	textStarted  bool
	textContent  strings.Builder
	toolCalls    map[int]*toolCallAccumulator
	finishReason string
	inputTokens  int
	outputTokens int
	sawToolUse   bool
}

// toolCallAccumulator accumulates a single tool call.
type toolCallAccumulator struct {
	Name                 string
	Args                 map[string]any
	PartialInput         map[string]any
	PartialStringByPath  map[string]string
	ThoughtSignature     string
	announced            bool
	streamed             bool
	jsonStarted          bool
	jsonClosed           bool
	activeStringKey      string
	emittedKeyValuePairs int
}

// streamChunk represents a streaming chunk from Gemini.
type streamChunk struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
}

// newEventStream creates a new event stream from an HTTP response body.
func newEventStream(body io.ReadCloser, model string) *eventStream {
	return &eventStream{
		parser: sseframe.New(body),
		closer: body,
		model:  model,
		accumulator: streamAccumulator{
			toolCalls: make(map[int]*toolCallAccumulator),
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

	if s.finished {
		return nil, io.EOF
	}

	for {
		frame, err := s.parser.Next()
		if err != nil {
			if err == io.EOF {
				return s.finalizeAndBuildFinalEvent()
			}
			s.err = err
			return nil, err
		}

		data := strings.TrimSpace(string(frame.Data))
		if data == "" {
			continue
		}

		// Check for stream end markers
		if data == "[DONE]" || data == "" {
			return s.finalizeAndBuildFinalEvent()
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // Skip unparseable chunks
		}

		// Handle usage (may come at end)
		if chunk.UsageMetadata != nil {
			s.accumulator.inputTokens = chunk.UsageMetadata.PromptTokenCount
			s.accumulator.outputTokens = chunk.UsageMetadata.CandidatesTokenCount
		}

		// No candidates means this is just a usage update
		if len(chunk.Candidates) == 0 {
			continue
		}

		candidate := chunk.Candidates[0]

		// Handle finish reason
		if candidate.FinishReason != "" {
			s.accumulator.finishReason = candidate.FinishReason
		}

		var events []types.StreamEvent

		// Emit message_start if not yet started
		if !s.started {
			s.started = true
			events = append(events, types.MessageStartEvent{
				Type: "message_start",
				Message: types.MessageResponse{
					ID:      fmt.Sprintf("msg_%s", s.model),
					Type:    "message",
					Role:    "assistant",
					Model:   "gemini/" + s.model,
					Content: []types.ContentBlock{},
					Usage:   types.Usage{},
				},
			})
		}

		completeHints := make(map[int]struct{})

		// Process parts
		for partIdx, part := range candidate.Content.Parts {
			// Handle text delta
			if part.Text != "" {
				if !s.accumulator.textStarted {
					s.accumulator.textStarted = true
					events = append(events, types.ContentBlockStartEvent{
						Type:         "content_block_start",
						Index:        0,
						ContentBlock: types.TextBlock{Type: "text", Text: ""},
					})
				}
				s.accumulator.textContent.WriteString(part.Text)
				events = append(events, types.ContentBlockDeltaEvent{
					Type:  "content_block_delta",
					Index: 0,
					Delta: types.TextDelta{Type: "text_delta", Text: part.Text},
				})
			}

			if part.FunctionCall != nil {
				if doneHint := s.processFunctionCallPart(partIdx, part, &events); doneHint {
					completeHints[partIdx] = struct{}{}
				}
			}
		}

		if len(completeHints) > 0 {
			events = append(events, s.finalizeSelectedToolInputs(completeHints)...)
		}

		if candidate.FinishReason != "" {
			events = append(events, s.finalizeOpenToolInputs()...)
		}

		if len(events) > 0 {
			if len(events) > 1 {
				s.pending = append(s.pending, events[1:]...)
			}
			return events[0], nil
		}
	}
}

func (s *eventStream) processFunctionCallPart(partIdx int, part geminiPart, events *[]types.StreamEvent) bool {
	if part.FunctionCall == nil {
		return false
	}

	acc, ok := s.accumulator.toolCalls[partIdx]
	if !ok {
		acc = &toolCallAccumulator{}
		s.accumulator.toolCalls[partIdx] = acc
	}

	if part.FunctionCall.Name != "" {
		acc.Name = part.FunctionCall.Name
	}
	if part.FunctionCall.Args != nil {
		acc.Args = cloneAnyMap(part.FunctionCall.Args)
	}
	if part.ThoughtSignature != "" {
		acc.ThoughtSignature = part.ThoughtSignature
	}

	s.ensureToolStart(partIdx, acc, events)

	doneHint := boolPointerIsFalse(part.FunctionCall.WillContinue)

	for _, partial := range part.FunctionCall.PartialArgs {
		if wc, ok := partialWillContinue(partial); ok && !wc {
			doneHint = true
		}

		path := partialArgPath(partial)
		if path == "" {
			continue
		}

		rawValue, ok := partialArgValue(partial)
		if !ok {
			continue
		}

		if acc.PartialInput == nil {
			acc.PartialInput = make(map[string]any)
		}

		valueForAssembled := rawValue
		if frag, isString := rawValue.(string); isString {
			if acc.PartialStringByPath == nil {
				acc.PartialStringByPath = make(map[string]string)
			}
			acc.PartialStringByPath[path] += frag
			valueForAssembled = acc.PartialStringByPath[path]
		}

		if key, fragment, ok := rootStringPartial(path, rawValue); ok {
			delta := acc.appendRootStringFragment(key, fragment)
			if delta != "" {
				s.emitToolInputDelta(partIdx, delta, events)
			}
		}

		_ = setValueAtJSONPath(acc.PartialInput, path, valueForAssembled)
	}

	// Preserve one-shot behavior when the provider emits complete args without partial deltas.
	if len(part.FunctionCall.PartialArgs) == 0 && !acc.jsonStarted {
		input := assembledToolInput(acc)
		if len(input) > 0 {
			argsJSON, _ := json.Marshal(input)
			s.emitToolInputDelta(partIdx, string(argsJSON), events)
			acc.streamed = true
			acc.jsonStarted = true
			acc.jsonClosed = true
		}
	}

	return doneHint
}

func (s *eventStream) ensureToolStart(partIdx int, acc *toolCallAccumulator, events *[]types.StreamEvent) {
	if acc == nil || acc.announced || acc.Name == "" {
		return
	}

	acc.announced = true
	s.accumulator.sawToolUse = true
	*events = append(*events, types.ContentBlockStartEvent{
		Type:  "content_block_start",
		Index: s.toolIndex(partIdx),
		ContentBlock: types.ToolUseBlock{
			Type:  "tool_use",
			ID:    fmt.Sprintf("call_%s", acc.Name),
			Name:  acc.Name,
			Input: make(map[string]any),
		},
	})
}

func (s *eventStream) emitToolInputDelta(partIdx int, partialJSON string, events *[]types.StreamEvent) {
	if partialJSON == "" {
		return
	}
	*events = append(*events, types.ContentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: s.toolIndex(partIdx),
		Delta: types.InputJSONDelta{
			Type:        "input_json_delta",
			PartialJSON: partialJSON,
		},
	})
}

func (s *eventStream) finalizeSelectedToolInputs(selected map[int]struct{}) []types.StreamEvent {
	if len(selected) == 0 {
		return nil
	}
	keys := make([]int, 0, len(selected))
	for idx := range selected {
		keys = append(keys, idx)
	}
	sort.Ints(keys)

	var events []types.StreamEvent
	for _, idx := range keys {
		acc := s.accumulator.toolCalls[idx]
		events = append(events, s.finalizeToolInput(idx, acc)...)
	}
	return events
}

func (s *eventStream) finalizeOpenToolInputs() []types.StreamEvent {
	if len(s.accumulator.toolCalls) == 0 {
		return nil
	}
	keys := make([]int, 0, len(s.accumulator.toolCalls))
	for idx := range s.accumulator.toolCalls {
		keys = append(keys, idx)
	}
	sort.Ints(keys)

	var events []types.StreamEvent
	for _, idx := range keys {
		events = append(events, s.finalizeToolInput(idx, s.accumulator.toolCalls[idx])...)
	}
	return events
}

func (s *eventStream) finalizeToolInput(partIdx int, acc *toolCallAccumulator) []types.StreamEvent {
	if acc == nil || acc.jsonClosed {
		return nil
	}
	var events []types.StreamEvent
	if !acc.announced {
		if acc.Name == "" {
			return nil
		}
		acc.announced = true
		s.accumulator.sawToolUse = true
		events = append(events, types.ContentBlockStartEvent{
			Type:  "content_block_start",
			Index: s.toolIndex(partIdx),
			ContentBlock: types.ToolUseBlock{
				Type:  "tool_use",
				ID:    fmt.Sprintf("call_%s", acc.Name),
				Name:  acc.Name,
				Input: make(map[string]any),
			},
		})
	}

	if delta := acc.finalizeJSONFragment(); delta != "" {
		events = append(events, types.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: s.toolIndex(partIdx),
			Delta: types.InputJSONDelta{Type: "input_json_delta", PartialJSON: delta},
		})
	}

	return events
}

func (acc *toolCallAccumulator) finalizeJSONFragment() string {
	if acc == nil || acc.jsonClosed {
		return ""
	}

	input := assembledToolInput(acc)
	if !acc.jsonStarted {
		if len(input) == 0 {
			acc.jsonClosed = true
			return ""
		}
		argsJSON, _ := json.Marshal(input)
		acc.streamed = true
		acc.jsonStarted = true
		acc.jsonClosed = true
		return string(argsJSON)
	}

	var b strings.Builder
	if acc.activeStringKey != "" {
		b.WriteString(`"`)
		acc.activeStringKey = ""
	}

	if len(input) > 0 {
		keys := make([]string, 0, len(input))
		for k := range input {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if acc.emittedKeyValuePairs > 0 {
				b.WriteString(",")
			}
			keyJSON, _ := json.Marshal(k)
			valJSON, _ := json.Marshal(input[k])
			b.Write(keyJSON)
			b.WriteString(":")
			b.Write(valJSON)
			acc.emittedKeyValuePairs++
		}
	}

	b.WriteString("}")
	acc.jsonClosed = true
	acc.streamed = true
	return b.String()
}

func (acc *toolCallAccumulator) appendRootStringFragment(key, fragment string) string {
	if acc == nil || key == "" || acc.jsonClosed {
		return ""
	}

	var b strings.Builder
	if !acc.jsonStarted {
		b.WriteString("{")
		acc.jsonStarted = true
	}

	if acc.activeStringKey != key {
		if acc.activeStringKey != "" {
			b.WriteString(`"`)
			acc.activeStringKey = ""
		}
		if acc.emittedKeyValuePairs > 0 {
			b.WriteString(",")
		}
		keyJSON, _ := json.Marshal(key)
		b.Write(keyJSON)
		b.WriteString(`:"`)
		acc.emittedKeyValuePairs++
		acc.activeStringKey = key
	}

	b.WriteString(escapeJSONStringFragment(fragment))
	acc.streamed = true
	return b.String()
}

func assembledToolInput(acc *toolCallAccumulator) map[string]any {
	if acc == nil {
		return nil
	}

	out := make(map[string]any)
	mergeMapInto(out, acc.PartialInput)
	for k, v := range acc.Args {
		out[k] = deepCopyValue(v)
	}
	if acc.ThoughtSignature != "" {
		out["__thought_signature"] = acc.ThoughtSignature
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeMapInto(dst, src map[string]any) {
	if src == nil {
		return
	}
	for k, v := range src {
		srcMap, isMap := v.(map[string]any)
		if !isMap {
			dst[k] = deepCopyValue(v)
			continue
		}
		existing, ok := dst[k].(map[string]any)
		if !ok {
			existing = make(map[string]any)
			dst[k] = existing
		}
		mergeMapInto(existing, srcMap)
	}
}

func deepCopyValue(v any) any {
	if m, ok := v.(map[string]any); ok {
		copied := make(map[string]any, len(m))
		for k, nested := range m {
			copied[k] = deepCopyValue(nested)
		}
		return copied
	}
	return v
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func rootStringPartial(path string, value any) (key string, fragment string, ok bool) {
	frag, ok := value.(string)
	if !ok {
		return "", "", false
	}
	segments, ok := parseJSONPath(path)
	if !ok || len(segments) != 1 {
		return "", "", false
	}
	return segments[0], frag, true
}

func partialArgPath(arg geminiPartialArg) string {
	if arg.JSONPath != "" {
		return arg.JSONPath
	}
	return arg.JSONPathSnake
}

func partialArgValue(arg geminiPartialArg) (any, bool) {
	if arg.Value != nil {
		return arg.Value, true
	}
	if arg.StringValue != nil {
		return *arg.StringValue, true
	}
	if arg.StringValueSn != nil {
		return *arg.StringValueSn, true
	}
	if arg.NumberValue != nil {
		return *arg.NumberValue, true
	}
	if arg.NumberValueSn != nil {
		return *arg.NumberValueSn, true
	}
	if arg.BoolValue != nil {
		return *arg.BoolValue, true
	}
	if arg.BoolValueSn != nil {
		return *arg.BoolValueSn, true
	}
	if arg.NullValue || arg.NullValueSn {
		return nil, true
	}
	return nil, false
}

func partialWillContinue(arg geminiPartialArg) (bool, bool) {
	if arg.WillContinue != nil {
		return *arg.WillContinue, true
	}
	if arg.WillContinueS != nil {
		return *arg.WillContinueS, true
	}
	return false, false
}

func boolPointerIsFalse(v *bool) bool {
	return v != nil && !*v
}

func parseJSONPath(path string) ([]string, bool) {
	if !strings.HasPrefix(path, "$.") {
		return nil, false
	}
	raw := strings.TrimPrefix(path, "$.")
	if raw == "" || strings.ContainsAny(raw, "[]") {
		return nil, false
	}
	parts := strings.Split(raw, ".")
	segments := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, false
		}
		segments = append(segments, p)
	}
	return segments, true
}

func setValueAtJSONPath(dst map[string]any, path string, value any) bool {
	segments, ok := parseJSONPath(path)
	if !ok || len(segments) == 0 {
		return false
	}

	current := dst
	for i, segment := range segments {
		if i == len(segments)-1 {
			current[segment] = deepCopyValue(value)
			return true
		}
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[segment] = next
		}
		current = next
	}
	return false
}

func escapeJSONStringFragment(fragment string) string {
	encoded, _ := json.Marshal(fragment)
	if len(encoded) < 2 {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

func (s *eventStream) toolIndex(partIdx int) int {
	if s.accumulator.textStarted {
		return partIdx + 1
	}
	return partIdx
}

func (s *eventStream) finalizeAndBuildFinalEvent() (types.StreamEvent, error) {
	if s.finished {
		return nil, io.EOF
	}

	finalizationEvents := s.finalizeOpenToolInputs()
	if len(finalizationEvents) == 0 {
		return s.buildFinalEvent()
	}

	finalizationEvents = append(finalizationEvents, s.messageDeltaEvent())
	s.finished = true
	if len(finalizationEvents) > 1 {
		s.pending = append(s.pending, finalizationEvents[1:]...)
	}
	return finalizationEvents[0], nil
}

func (s *eventStream) messageDeltaEvent() types.MessageDeltaEvent {
	stopReason := mapFinishReason(s.accumulator.finishReason)
	if stopReason == types.StopReasonEndTurn && s.accumulator.sawToolUse {
		stopReason = types.StopReasonToolUse
	}

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
	}
}

// buildFinalEvent builds the final events when stream ends.
func (s *eventStream) buildFinalEvent() (types.StreamEvent, error) {
	if s.finished {
		return nil, io.EOF
	}

	s.finished = true
	return s.messageDeltaEvent(), nil
}

// Close releases resources associated with the stream.
func (s *eventStream) Close() error {
	return s.closer.Close()
}
