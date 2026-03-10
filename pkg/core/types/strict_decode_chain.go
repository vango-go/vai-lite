package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func UnmarshalChainClientFrameStrict(data []byte) (ChainClientFrame, error) {
	var typeHolder struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeHolder); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(typeHolder.Type) {
	case "chain.start":
		return unmarshalChainStartFrameStrict(data)
	case "chain.attach":
		return unmarshalChainAttachFrameStrict(data)
	case "chain.update":
		return unmarshalChainUpdateFrameStrict(data)
	case "run.start":
		return unmarshalRunStartFrameStrict(data)
	case "client_tool.result":
		return unmarshalClientToolResultFrameStrict(data)
	case "run.cancel":
		return unmarshalRunCancelFrameStrict(data)
	case "chain.close":
		return unmarshalChainCloseFrameStrict(data)
	default:
		return nil, strictErr("type", fmt.Sprintf("unknown chain client frame type %q", typeHolder.Type))
	}
}

func UnmarshalChainServerEventStrict(data []byte) (ChainServerEvent, error) {
	var typeHolder struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeHolder); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(typeHolder.Type) {
	case "chain.started":
		return unmarshalChainStartedEventStrict(data)
	case "chain.attached":
		return unmarshalChainAttachedEventStrict(data)
	case "chain.updated":
		return unmarshalChainUpdatedEventStrict(data)
	case "run.event":
		return unmarshalRunEnvelopeEventStrict(data)
	case "client_tool.call":
		return unmarshalClientToolCallEventStrict(data)
	case "chain.error":
		return unmarshalChainErrorEventStrict(data)
	default:
		return nil, strictErr("type", fmt.Sprintf("unknown chain server event type %q", typeHolder.Type))
	}
}

func UnmarshalChainStartPayloadStrict(data []byte) (*ChainStartPayload, error) {
	top, err := decodeJSONObjectStrict(data)
	if err != nil {
		return nil, err
	}
	payload, err := parseChainStartPayloadStrict(top, "")
	if err != nil {
		return nil, err
	}
	return &payload, nil
}

func UnmarshalChainUpdatePayloadStrict(data []byte) (*ChainUpdatePayload, error) {
	top, err := decodeJSONObjectStrict(data)
	if err != nil {
		return nil, err
	}
	payload, err := parseChainUpdatePayloadStrict(top, "")
	if err != nil {
		return nil, err
	}
	return &payload, nil
}

func UnmarshalChainRunPayloadStrict(data []byte) (*RunStartPayload, error) {
	top, err := decodeJSONObjectStrict(data)
	if err != nil {
		return nil, err
	}
	payload, err := parseRunStartPayloadStrict(top, "")
	if err != nil {
		return nil, err
	}
	return &payload, nil
}

func unmarshalChainStartFrameStrict(data []byte) (ChainClientFrame, error) {
	top, err := decodeJSONObjectStrict(data)
	if err != nil {
		return nil, err
	}
	allowed := setOfKeys("type", "idempotency_key", "external_session_id", "defaults", "history", "metadata")
	if err := rejectUnknownKeys(top, allowed, ""); err != nil {
		return nil, err
	}
	key := decodeRequiredStringField(top, "idempotency_key")
	if key == "" {
		return nil, strictErr("idempotency_key", "idempotency_key is required")
	}
	payload, err := parseChainStartPayloadStrict(top, "")
	if err != nil {
		return nil, err
	}
	return ChainStartFrame{
		Type:              "chain.start",
		IdempotencyKey:    key,
		ChainStartPayload: payload,
	}, nil
}

func unmarshalChainAttachFrameStrict(data []byte) (ChainClientFrame, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var frame ChainAttachFrame
	if err := dec.Decode(&frame); err != nil {
		return nil, strictErr("frame", fmt.Sprintf("invalid chain attach frame: %v", err))
	}
	if strings.TrimSpace(frame.IdempotencyKey) == "" {
		return nil, strictErr("idempotency_key", "idempotency_key is required")
	}
	if strings.TrimSpace(frame.ChainID) == "" {
		return nil, strictErr("chain_id", "chain_id is required")
	}
	if strings.TrimSpace(frame.ResumeToken) == "" {
		return nil, strictErr("resume_token", "resume_token is required")
	}
	frame.Type = "chain.attach"
	return frame, nil
}

func unmarshalChainUpdateFrameStrict(data []byte) (ChainClientFrame, error) {
	top, err := decodeJSONObjectStrict(data)
	if err != nil {
		return nil, err
	}
	allowed := setOfKeys("type", "idempotency_key", "defaults")
	if err := rejectUnknownKeys(top, allowed, ""); err != nil {
		return nil, err
	}
	key := decodeRequiredStringField(top, "idempotency_key")
	if key == "" {
		return nil, strictErr("idempotency_key", "idempotency_key is required")
	}
	payload, err := parseChainUpdatePayloadStrict(top, "")
	if err != nil {
		return nil, err
	}
	return ChainUpdateFrame{
		Type:               "chain.update",
		IdempotencyKey:     key,
		ChainUpdatePayload: payload,
	}, nil
}

func unmarshalRunStartFrameStrict(data []byte) (ChainClientFrame, error) {
	top, err := decodeJSONObjectStrict(data)
	if err != nil {
		return nil, err
	}
	allowed := setOfKeys("type", "idempotency_key", "input", "overrides", "metadata")
	if err := rejectUnknownKeys(top, allowed, ""); err != nil {
		return nil, err
	}
	key := decodeRequiredStringField(top, "idempotency_key")
	if key == "" {
		return nil, strictErr("idempotency_key", "idempotency_key is required")
	}
	payload, err := parseRunStartPayloadStrict(top, "")
	if err != nil {
		return nil, err
	}
	return RunStartFrame{
		Type:            "run.start",
		IdempotencyKey:  key,
		RunStartPayload: payload,
	}, nil
}

func unmarshalClientToolResultFrameStrict(data []byte) (ChainClientFrame, error) {
	var raw struct {
		Type           string            `json:"type"`
		IdempotencyKey string            `json:"idempotency_key"`
		RunID          string            `json:"run_id"`
		ExecutionID    string            `json:"execution_id"`
		Content        []json.RawMessage `json:"content,omitempty"`
		IsError        bool              `json:"is_error,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, strictErr("frame", fmt.Sprintf("invalid client tool result frame: %v", err))
	}
	if strings.TrimSpace(raw.IdempotencyKey) == "" {
		return nil, strictErr("idempotency_key", "idempotency_key is required")
	}
	if strings.TrimSpace(raw.RunID) == "" {
		return nil, strictErr("run_id", "run_id is required")
	}
	if strings.TrimSpace(raw.ExecutionID) == "" {
		return nil, strictErr("execution_id", "execution_id is required")
	}
	content, err := unmarshalContentBlocks(raw.Content)
	if err != nil {
		return nil, err
	}
	return ClientToolResultFrame{
		Type:           "client_tool.result",
		IdempotencyKey: raw.IdempotencyKey,
		RunID:          raw.RunID,
		ExecutionID:    raw.ExecutionID,
		Content:        content,
		IsError:        raw.IsError,
	}, nil
}

func unmarshalRunCancelFrameStrict(data []byte) (ChainClientFrame, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var frame RunCancelFrame
	if err := dec.Decode(&frame); err != nil {
		return nil, strictErr("frame", fmt.Sprintf("invalid run cancel frame: %v", err))
	}
	if strings.TrimSpace(frame.IdempotencyKey) == "" {
		return nil, strictErr("idempotency_key", "idempotency_key is required")
	}
	if strings.TrimSpace(frame.RunID) == "" {
		return nil, strictErr("run_id", "run_id is required")
	}
	frame.Type = "run.cancel"
	return frame, nil
}

func unmarshalChainCloseFrameStrict(data []byte) (ChainClientFrame, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var frame ChainCloseFrame
	if err := dec.Decode(&frame); err != nil {
		return nil, strictErr("frame", fmt.Sprintf("invalid chain close frame: %v", err))
	}
	if strings.TrimSpace(frame.IdempotencyKey) == "" {
		return nil, strictErr("idempotency_key", "idempotency_key is required")
	}
	frame.Type = "chain.close"
	return frame, nil
}

func unmarshalChainStartedEventStrict(data []byte) (ChainServerEvent, error) {
	var raw struct {
		Type                   string          `json:"type"`
		EventID                int64           `json:"event_id"`
		ChainVersion           int64           `json:"chain_version"`
		ChainID                string          `json:"chain_id"`
		SessionID              string          `json:"session_id,omitempty"`
		ExternalSessionID      string          `json:"external_session_id,omitempty"`
		ResumeToken            string          `json:"resume_token"`
		ActorID                string          `json:"actor_id,omitempty"`
		AuthorizedProviders    []string        `json:"authorized_providers,omitempty"`
		AuthorizedGatewayTools []string        `json:"authorized_gateway_tools,omitempty"`
		Defaults               json.RawMessage `json:"defaults,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, strictErr("event", fmt.Sprintf("invalid chain.started event: %v", err))
	}
	defaults, err := unmarshalChainDefaultsStrict(raw.Defaults, "defaults")
	if err != nil {
		return nil, err
	}
	return ChainStartedEvent{
		Type:                   "chain.started",
		EventID:                raw.EventID,
		ChainVersion:           raw.ChainVersion,
		ChainID:                raw.ChainID,
		SessionID:              raw.SessionID,
		ExternalSessionID:      raw.ExternalSessionID,
		ResumeToken:            raw.ResumeToken,
		ActorID:                raw.ActorID,
		AuthorizedProviders:    raw.AuthorizedProviders,
		AuthorizedGatewayTools: raw.AuthorizedGatewayTools,
		Defaults:               defaults,
	}, nil
}

func unmarshalChainAttachedEventStrict(data []byte) (ChainServerEvent, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var event ChainAttachedEvent
	if err := dec.Decode(&event); err != nil {
		return nil, strictErr("event", fmt.Sprintf("invalid chain.attached event: %v", err))
	}
	event.Type = "chain.attached"
	return event, nil
}

func unmarshalChainUpdatedEventStrict(data []byte) (ChainServerEvent, error) {
	var raw struct {
		Type         string          `json:"type"`
		EventID      int64           `json:"event_id"`
		ChainVersion int64           `json:"chain_version"`
		ChainID      string          `json:"chain_id"`
		Defaults     json.RawMessage `json:"defaults,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, strictErr("event", fmt.Sprintf("invalid chain.updated event: %v", err))
	}
	defaults, err := unmarshalChainDefaultsStrict(raw.Defaults, "defaults")
	if err != nil {
		return nil, err
	}
	return ChainUpdatedEvent{
		Type:         "chain.updated",
		EventID:      raw.EventID,
		ChainVersion: raw.ChainVersion,
		ChainID:      raw.ChainID,
		Defaults:     defaults,
	}, nil
}

func unmarshalRunEnvelopeEventStrict(data []byte) (ChainServerEvent, error) {
	var raw struct {
		Type         string          `json:"type"`
		EventID      int64           `json:"event_id"`
		ChainVersion int64           `json:"chain_version"`
		RunID        string          `json:"run_id"`
		ChainID      string          `json:"chain_id"`
		Event        json.RawMessage `json:"event"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, strictErr("event", fmt.Sprintf("invalid run.event event: %v", err))
	}
	nested, err := UnmarshalRunStreamEvent(raw.Event)
	if err != nil {
		return nil, err
	}
	if _, unknown := nested.(UnknownRunStreamEvent); unknown {
		return nil, strictErr("event.type", "unknown nested run event type")
	}
	return RunEnvelopeEvent{
		Type:         "run.event",
		EventID:      raw.EventID,
		ChainVersion: raw.ChainVersion,
		RunID:        raw.RunID,
		ChainID:      raw.ChainID,
		Event:        nested,
	}, nil
}

func unmarshalClientToolCallEventStrict(data []byte) (ChainServerEvent, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var event ClientToolCallEvent
	if err := dec.Decode(&event); err != nil {
		return nil, strictErr("event", fmt.Sprintf("invalid client_tool.call event: %v", err))
	}
	event.Type = "client_tool.call"
	return event, nil
}

func unmarshalChainErrorEventStrict(data []byte) (ChainServerEvent, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var event ChainErrorEvent
	if err := dec.Decode(&event); err != nil {
		return nil, strictErr("event", fmt.Sprintf("invalid chain.error event: %v", err))
	}
	event.Type = "chain.error"
	return event, nil
}

func parseChainStartPayloadStrict(top map[string]json.RawMessage, prefix string) (ChainStartPayload, error) {
	payload := ChainStartPayload{
		ExternalSessionID: decodeOptionalStringField(top, "external_session_id"),
	}
	defaults, err := unmarshalChainDefaultsStrict(top["defaults"], prefixPath(prefix, "defaults"))
	if err != nil {
		return ChainStartPayload{}, err
	}
	payload.Defaults = defaults
	history, err := unmarshalChainMessagesStrict(top["history"], prefixPath(prefix, "history"), false)
	if err != nil {
		return ChainStartPayload{}, err
	}
	payload.History = history
	metadata, err := unmarshalLooseObjectStrict(top["metadata"], prefixPath(prefix, "metadata"))
	if err != nil {
		return ChainStartPayload{}, err
	}
	payload.Metadata = metadata
	return payload, nil
}

func parseChainUpdatePayloadStrict(top map[string]json.RawMessage, prefix string) (ChainUpdatePayload, error) {
	defaults, err := unmarshalChainDefaultsStrict(top["defaults"], prefixPath(prefix, "defaults"))
	if err != nil {
		return ChainUpdatePayload{}, err
	}
	return ChainUpdatePayload{Defaults: defaults}, nil
}

func parseRunStartPayloadStrict(top map[string]json.RawMessage, prefix string) (RunStartPayload, error) {
	input, err := unmarshalChainMessagesStrict(top["input"], prefixPath(prefix, "input"), true)
	if err != nil {
		return RunStartPayload{}, err
	}
	var overrides *RunOverrides
	if !isNullOrEmptyJSON(top["overrides"]) {
		value, err := unmarshalChainDefaultsStrict(top["overrides"], prefixPath(prefix, "overrides"))
		if err != nil {
			return RunStartPayload{}, err
		}
		overrides = &value
	}
	metadata, err := unmarshalLooseObjectStrict(top["metadata"], prefixPath(prefix, "metadata"))
	if err != nil {
		return RunStartPayload{}, err
	}
	return RunStartPayload{
		Input:     input,
		Overrides: overrides,
		Metadata:  metadata,
	}, nil
}

func unmarshalChainMessagesStrict(raw json.RawMessage, param string, required bool) ([]Message, error) {
	if isNullOrEmptyJSON(raw) {
		if required {
			return nil, strictErr(param, param+" is required")
		}
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, strictErr(param, param+" must be an array of messages")
	}
	if required && len(items) == 0 {
		return nil, strictErr(param, param+" must not be empty")
	}
	out := make([]Message, len(items))
	for i := range items {
		msg, err := unmarshalMessageStrict(items[i], fmt.Sprintf("%s[%d]", param, i))
		if err != nil {
			return nil, err
		}
		out[i] = msg
	}
	return out, nil
}

func unmarshalChainDefaultsStrict(raw json.RawMessage, param string) (ChainDefaults, error) {
	if isNullOrEmptyJSON(raw) {
		return ChainDefaults{}, nil
	}
	top, err := decodeJSONObjectStrict(raw)
	if err != nil {
		return ChainDefaults{}, strictErr(param, param+" must be an object")
	}
	allowed := setOfKeys(
		"model", "system", "tools", "gateway_tools", "gateway_tool_config", "tool_choice",
		"max_tokens", "temperature", "top_p", "top_k", "stop_sequences",
		"stt_model", "tts_model", "output_format", "output", "voice", "extensions", "metadata",
	)
	if err := rejectUnknownKeys(top, allowed, param); err != nil {
		return ChainDefaults{}, err
	}
	var out ChainDefaults
	out.Model = decodeOptionalStringField(top, "model")
	if out.Model != "" {
		if err := validateProviderModelIDStrict(out.Model, prefixPath(param, "model")); err != nil {
			return ChainDefaults{}, err
		}
	}
	if !isNullOrEmptyJSON(top["system"]) {
		system, err := unmarshalSystemStrict(top["system"])
		if err != nil {
			if se, ok := err.(*StrictDecodeError); ok && se.Param != "" {
				if strings.HasPrefix(se.Param, "[") {
					se.Param = prefixPath(param, "system") + se.Param
				} else {
					se.Param = prefixPath(param, "system") + "." + se.Param
				}
				return ChainDefaults{}, se
			}
			return ChainDefaults{}, strictErr(prefixPath(param, "system"), "system must be a string or an array of content blocks")
		}
		out.System = system
	}
	if !isNullOrEmptyJSON(top["tools"]) {
		var items []json.RawMessage
		if err := json.Unmarshal(top["tools"], &items); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "tools"), "tools must be an array")
		}
		out.Tools = make([]Tool, len(items))
		for i := range items {
			tool, err := unmarshalToolStrict(items[i], fmt.Sprintf("%s[%d]", prefixPath(param, "tools"), i))
			if err != nil {
				return ChainDefaults{}, err
			}
			out.Tools[i] = tool
		}
	}
	if !isNullOrEmptyJSON(top["gateway_tools"]) {
		names, err := parseRunToolNames(top["gateway_tools"], prefixPath(param, "gateway_tools"))
		if err != nil {
			return ChainDefaults{}, err
		}
		out.GatewayTools = names
	}
	cfg, err := unmarshalGatewayToolConfigStrict(top["gateway_tool_config"], prefixPath(param, "gateway_tool_config"))
	if err != nil {
		return ChainDefaults{}, err
	}
	out.GatewayToolConfig = cfg
	if !isNullOrEmptyJSON(top["tool_choice"]) {
		dec := json.NewDecoder(bytes.NewReader(top["tool_choice"]))
		dec.DisallowUnknownFields()
		var choice ToolChoice
		if err := dec.Decode(&choice); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "tool_choice"), fmt.Sprintf("invalid tool_choice: %v", err))
		}
		out.ToolChoice = &choice
	}
	if !isNullOrEmptyJSON(top["max_tokens"]) {
		if err := json.Unmarshal(top["max_tokens"], &out.MaxTokens); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "max_tokens"), "max_tokens must be an integer")
		}
	}
	if !isNullOrEmptyJSON(top["temperature"]) {
		out.Temperature = new(float64)
		if err := json.Unmarshal(top["temperature"], out.Temperature); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "temperature"), "temperature must be a number")
		}
	}
	if !isNullOrEmptyJSON(top["top_p"]) {
		out.TopP = new(float64)
		if err := json.Unmarshal(top["top_p"], out.TopP); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "top_p"), "top_p must be a number")
		}
	}
	if !isNullOrEmptyJSON(top["top_k"]) {
		out.TopK = new(int)
		if err := json.Unmarshal(top["top_k"], out.TopK); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "top_k"), "top_k must be an integer")
		}
	}
	if !isNullOrEmptyJSON(top["stop_sequences"]) {
		if err := json.Unmarshal(top["stop_sequences"], &out.StopSequences); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "stop_sequences"), "stop_sequences must be an array of strings")
		}
	}
	out.STTModel = decodeOptionalStringField(top, "stt_model")
	if out.STTModel != "" {
		if err := validateProviderModelIDStrict(out.STTModel, prefixPath(param, "stt_model")); err != nil {
			return ChainDefaults{}, err
		}
	}
	out.TTSModel = decodeOptionalStringField(top, "tts_model")
	if out.TTSModel != "" {
		if err := validateProviderModelIDStrict(out.TTSModel, prefixPath(param, "tts_model")); err != nil {
			return ChainDefaults{}, err
		}
	}
	if !isNullOrEmptyJSON(top["output_format"]) {
		dec := json.NewDecoder(bytes.NewReader(top["output_format"]))
		dec.DisallowUnknownFields()
		var format OutputFormat
		if err := dec.Decode(&format); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "output_format"), fmt.Sprintf("invalid output_format: %v", err))
		}
		out.OutputFormat = &format
	}
	if !isNullOrEmptyJSON(top["output"]) {
		dec := json.NewDecoder(bytes.NewReader(top["output"]))
		dec.DisallowUnknownFields()
		var output OutputConfig
		if err := dec.Decode(&output); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "output"), fmt.Sprintf("invalid output config: %v", err))
		}
		out.Output = &output
	}
	if !isNullOrEmptyJSON(top["voice"]) {
		var voiceFields map[string]json.RawMessage
		if err := json.Unmarshal(top["voice"], &voiceFields); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "voice"), "voice must be an object")
		}
		if _, hasInput := voiceFields["input"]; hasInput {
			return ChainDefaults{}, strictErr(prefixPath(param, "voice.input"), "voice.input has been removed; use audio_stt blocks and top-level stt_model")
		}
		dec := json.NewDecoder(bytes.NewReader(top["voice"]))
		dec.DisallowUnknownFields()
		var voiceCfg VoiceConfig
		if err := dec.Decode(&voiceCfg); err != nil {
			return ChainDefaults{}, strictErr(prefixPath(param, "voice"), fmt.Sprintf("invalid voice config: %v", err))
		}
		out.Voice = &voiceCfg
	}
	extensions, err := unmarshalLooseObjectStrict(top["extensions"], prefixPath(param, "extensions"))
	if err != nil {
		return ChainDefaults{}, err
	}
	out.Extensions = extensions
	metadata, err := unmarshalLooseObjectStrict(top["metadata"], prefixPath(param, "metadata"))
	if err != nil {
		return ChainDefaults{}, err
	}
	out.Metadata = metadata
	return out, nil
}

func unmarshalSystemStrict(raw json.RawMessage) (any, error) {
	var sysStr string
	if err := json.Unmarshal(raw, &sysStr); err == nil {
		return sysStr, nil
	}
	blocks, err := UnmarshalContentBlocksStrict(raw)
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

func unmarshalGatewayToolConfigStrict(raw json.RawMessage, param string) (map[string]any, error) {
	if isNullOrEmptyJSON(raw) {
		return nil, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, strictErr(param, param+" must be an object")
	}
	out := make(map[string]any, len(top))
	for key, value := range top {
		name := strings.TrimSpace(key)
		if name == "" {
			return nil, strictErr(param, param+" keys must be non-empty")
		}
		if isNullOrEmptyJSON(value) {
			return nil, strictErr(prefixPath(param, name), "gateway tool config entries must be objects")
		}
		var decoded map[string]any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, strictErr(prefixPath(param, name), "gateway tool config entries must be objects")
		}
		out[name] = decoded
	}
	return out, nil
}

func unmarshalLooseObjectStrict(raw json.RawMessage, param string) (map[string]any, error) {
	if isNullOrEmptyJSON(raw) {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, strictErr(param, param+" must be an object")
	}
	return out, nil
}

func decodeJSONObjectStrict(data []byte) (map[string]json.RawMessage, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	return top, nil
}

func setOfKeys(keys ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		out[key] = struct{}{}
	}
	return out
}

func rejectUnknownKeys(top map[string]json.RawMessage, allowed map[string]struct{}, prefix string) error {
	for key := range top {
		if _, ok := allowed[key]; ok {
			continue
		}
		if prefix == "" {
			return strictErr(key, fmt.Sprintf("unknown field %q", key))
		}
		return strictErr(prefixPath(prefix, key), fmt.Sprintf("unknown field %q", key))
	}
	return nil
}

func decodeRequiredStringField(top map[string]json.RawMessage, key string) string {
	return decodeOptionalStringField(top, key)
}

func decodeOptionalStringField(top map[string]json.RawMessage, key string) string {
	raw, ok := top[key]
	if !ok || isNullOrEmptyJSON(raw) {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func prefixPath(prefix, field string) string {
	switch {
	case prefix == "":
		return field
	case field == "":
		return prefix
	default:
		return prefix + "." + field
	}
}
