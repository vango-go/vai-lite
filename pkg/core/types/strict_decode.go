package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// StrictDecodeError is returned when strict request decoding fails.
// It includes an optional Param field suitable for API error reporting.
type StrictDecodeError struct {
	Param   string
	Message string
}

func (e *StrictDecodeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Param != "" {
		return fmt.Sprintf("%s: %s", e.Param, e.Message)
	}
	return e.Message
}

func strictErr(param, msg string) error {
	return &StrictDecodeError{Param: param, Message: msg}
}

var allowedJSONSchemaTypes = map[string]struct{}{
	"object":  {},
	"array":   {},
	"string":  {},
	"number":  {},
	"integer": {},
	"boolean": {},
	"null":    {},
}

func isNullOrEmptyJSON(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

// UnmarshalContentBlockStrict deserializes a content block from JSON and rejects unknown types.
func UnmarshalContentBlockStrict(data []byte) (ContentBlock, error) {
	var typeHolder struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeHolder); err != nil {
		return nil, err
	}

	switch typeHolder.Type {
	case "text":
		var block TextBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case "image":
		var block ImageBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case "audio":
		var block AudioBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case "video":
		var block VideoBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case "document":
		var block DocumentBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case "tool_result":
		var raw struct {
			Type      string            `json:"type"`
			ToolUseID string            `json:"tool_use_id"`
			Content   []json.RawMessage `json:"content"`
			IsError   bool              `json:"is_error"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		if strings.TrimSpace(raw.ToolUseID) == "" {
			return nil, strictErr("tool_use_id", "tool_result.tool_use_id is required")
		}
		block := ToolResultBlock{
			Type:      raw.Type,
			ToolUseID: raw.ToolUseID,
			IsError:   raw.IsError,
		}
		for _, c := range raw.Content {
			cb, err := UnmarshalContentBlockStrict(c)
			if err != nil {
				return nil, err
			}
			block.Content = append(block.Content, cb)
		}
		return block, nil
	case "tool_use":
		var block ToolUseBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		if strings.TrimSpace(block.ID) == "" {
			return nil, strictErr("id", "tool_use.id is required")
		}
		if strings.TrimSpace(block.Name) == "" {
			return nil, strictErr("name", "tool_use.name is required")
		}
		if block.Input == nil {
			return nil, strictErr("input", "tool_use.input is required")
		}
		return block, nil
	case "thinking":
		var block ThinkingBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		return block, nil
	case "server_tool_use":
		var block ServerToolUseBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		if strings.TrimSpace(block.ID) == "" {
			return nil, strictErr("id", "server_tool_use.id is required")
		}
		if strings.TrimSpace(block.Name) == "" {
			return nil, strictErr("name", "server_tool_use.name is required")
		}
		if block.Input == nil {
			return nil, strictErr("input", "server_tool_use.input is required")
		}
		return block, nil
	case "web_search_tool_result":
		// Provider-emitted block; accept for completeness (still strict on type).
		var block WebSearchToolResultBlock
		if err := json.Unmarshal(data, &block); err != nil {
			return nil, err
		}
		if strings.TrimSpace(block.ToolUseID) == "" {
			return nil, strictErr("tool_use_id", "web_search_tool_result.tool_use_id is required")
		}
		return block, nil
	default:
		return nil, strictErr("type", fmt.Sprintf("unknown content block type %q", typeHolder.Type))
	}
}

// UnmarshalContentBlocksStrict deserializes a slice of content blocks from JSON and rejects unknown types.
func UnmarshalContentBlocksStrict(data []byte) ([]ContentBlock, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	blocks := make([]ContentBlock, len(raw))
	for i, r := range raw {
		block, err := UnmarshalContentBlockStrict(r)
		if err != nil {
			if se, ok := err.(*StrictDecodeError); ok && se.Param != "" {
				se.Param = fmt.Sprintf("[%d].%s", i, se.Param)
				return nil, se
			}
			return nil, err
		}
		blocks[i] = block
	}
	return blocks, nil
}

func unmarshalMessageStrict(data []byte, paramPrefix string) (Message, error) {
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Message{}, err
	}
	if strings.TrimSpace(raw.Role) == "" {
		return Message{}, strictErr(paramPrefix+".role", "role is required")
	}
	role := strings.TrimSpace(raw.Role)
	if role != "user" && role != "assistant" {
		return Message{}, strictErr(paramPrefix+".role", "role must be one of: user, assistant")
	}

	msg := Message{Role: role}

	// content: string or []ContentBlock
	var str string
	if err := json.Unmarshal(raw.Content, &str); err == nil {
		msg.Content = str
		return msg, nil
	}

	var blocksRaw []json.RawMessage
	if err := json.Unmarshal(raw.Content, &blocksRaw); err != nil {
		return Message{}, strictErr(paramPrefix+".content", "content must be a string or an array of content blocks")
	}
	blocks := make([]ContentBlock, len(blocksRaw))
	for i, b := range blocksRaw {
		cb, err := UnmarshalContentBlockStrict(b)
		if err != nil {
			if se, ok := err.(*StrictDecodeError); ok {
				se.Param = fmt.Sprintf("%s.content[%d].%s", paramPrefix, i, se.Param)
				return Message{}, se
			}
			return Message{}, err
		}
		if err := validateContentBlockRoleStrict(role, cb, fmt.Sprintf("%s.content[%d].type", paramPrefix, i)); err != nil {
			return Message{}, err
		}
		blocks[i] = cb
	}
	msg.Content = blocks
	return msg, nil
}

func unmarshalToolStrict(data []byte, paramPrefix string) (Tool, error) {
	var raw struct {
		Type        string          `json:"type"`
		Name        string          `json:"name,omitempty"`
		Description string          `json:"description,omitempty"`
		InputSchema *JSONSchema     `json:"input_schema,omitempty"`
		Config      json.RawMessage `json:"config,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Tool{}, err
	}

	if strings.TrimSpace(raw.Type) == "" {
		return Tool{}, strictErr(paramPrefix+".type", "tool.type is required")
	}

	out := Tool{
		Type:        raw.Type,
		Name:        raw.Name,
		Description: raw.Description,
		InputSchema: raw.InputSchema,
	}

	cfgPresent := !isNullOrEmptyJSON(raw.Config)
	switch raw.Type {
	case ToolTypeWebSearch:
		if cfgPresent {
			var cfg WebSearchConfig
			if err := json.Unmarshal(raw.Config, &cfg); err != nil {
				return Tool{}, strictErr(paramPrefix+".config", fmt.Sprintf("invalid web_search config: %v", err))
			}
			out.Config = &cfg
		}
	case ToolTypeWebFetch:
		if cfgPresent {
			var cfg WebFetchConfig
			if err := json.Unmarshal(raw.Config, &cfg); err != nil {
				return Tool{}, strictErr(paramPrefix+".config", fmt.Sprintf("invalid web_fetch config: %v", err))
			}
			out.Config = &cfg
		}
	case ToolTypeCodeExecution:
		if cfgPresent {
			var cfg CodeExecutionConfig
			if err := json.Unmarshal(raw.Config, &cfg); err != nil {
				return Tool{}, strictErr(paramPrefix+".config", fmt.Sprintf("invalid code_execution config: %v", err))
			}
			out.Config = &cfg
		}
	case ToolTypeComputerUse:
		if cfgPresent {
			var cfg ComputerUseConfig
			if err := json.Unmarshal(raw.Config, &cfg); err != nil {
				return Tool{}, strictErr(paramPrefix+".config", fmt.Sprintf("invalid computer_use config: %v", err))
			}
			out.Config = &cfg
		}
	case ToolTypeFileSearch:
		if cfgPresent {
			var cfg FileSearchConfig
			if err := json.Unmarshal(raw.Config, &cfg); err != nil {
				return Tool{}, strictErr(paramPrefix+".config", fmt.Sprintf("invalid file_search config: %v", err))
			}
			out.Config = &cfg
		}
	case ToolTypeTextEditor:
		if cfgPresent {
			var cfg TextEditorConfig
			if err := json.Unmarshal(raw.Config, &cfg); err != nil {
				return Tool{}, strictErr(paramPrefix+".config", fmt.Sprintf("invalid text_editor config: %v", err))
			}
			out.Config = &cfg
		}
	case ToolTypeFunction:
		if cfgPresent {
			return Tool{}, strictErr(paramPrefix+".config", "function tool config must be null or omitted")
		}
		if strings.TrimSpace(out.Name) == "" {
			return Tool{}, strictErr(paramPrefix+".name", "function tool name is required")
		}
		if strings.TrimSpace(out.Description) == "" {
			return Tool{}, strictErr(paramPrefix+".description", "function tool description is required")
		}
		if out.InputSchema == nil {
			return Tool{}, strictErr(paramPrefix+".input_schema", "function tool input_schema is required")
		}
	default:
		return Tool{}, strictErr(paramPrefix+".type", fmt.Sprintf("unknown tool type %q", raw.Type))
	}

	return out, nil
}

func validateToolHistoryStrict(messages []Message) error {
	seenToolUses := make(map[string]struct{})
	for mi, msg := range messages {
		for bi, block := range msg.ContentBlocks() {
			switch b := block.(type) {
			case ToolUseBlock:
				seenToolUses[b.ID] = struct{}{}
			case *ToolUseBlock:
				if b != nil {
					seenToolUses[b.ID] = struct{}{}
				}
			case ServerToolUseBlock:
				seenToolUses[b.ID] = struct{}{}
			case *ServerToolUseBlock:
				if b != nil {
					seenToolUses[b.ID] = struct{}{}
				}
			case ToolResultBlock:
				if strings.TrimSpace(b.ToolUseID) == "" {
					return strictErr(fmt.Sprintf("messages[%d].content[%d].tool_use_id", mi, bi), "tool_result.tool_use_id is required")
				}
				if _, ok := seenToolUses[b.ToolUseID]; !ok {
					return strictErr(fmt.Sprintf("messages[%d].content[%d].tool_use_id", mi, bi), "tool_result.tool_use_id does not match any prior tool_use.id")
				}
			case *ToolResultBlock:
				if b == nil {
					continue
				}
				if strings.TrimSpace(b.ToolUseID) == "" {
					return strictErr(fmt.Sprintf("messages[%d].content[%d].tool_use_id", mi, bi), "tool_result.tool_use_id is required")
				}
				if _, ok := seenToolUses[b.ToolUseID]; !ok {
					return strictErr(fmt.Sprintf("messages[%d].content[%d].tool_use_id", mi, bi), "tool_result.tool_use_id does not match any prior tool_use.id")
				}
			}
		}
	}
	return nil
}

// UnmarshalMessageRequestStrict strictly decodes a MessageRequest suitable for proxy request bodies.
// It enforces:
// - union shapes for system and message content (string or []ContentBlock)
// - strict content block types (unknown types rejected)
// - tool config decoding into typed config structs
// - tool history validation (tool_result.tool_use_id must match a prior tool_use.id)
func UnmarshalMessageRequestStrict(data []byte) (*MessageRequest, error) {
	var raw struct {
		Model         string            `json:"model"`
		Messages      []json.RawMessage `json:"messages"`
		MaxTokens     int               `json:"max_tokens,omitempty"`
		System        json.RawMessage   `json:"system,omitempty"`
		Temperature   *float64          `json:"temperature,omitempty"`
		TopP          *float64          `json:"top_p,omitempty"`
		TopK          *int              `json:"top_k,omitempty"`
		StopSequences []string          `json:"stop_sequences,omitempty"`
		Tools         []json.RawMessage `json:"tools,omitempty"`
		ToolChoice    *ToolChoice       `json:"tool_choice,omitempty"`
		Stream        bool              `json:"stream,omitempty"`
		OutputFormat  *OutputFormat     `json:"output_format,omitempty"`
		Output        *OutputConfig     `json:"output,omitempty"`
		Voice         *VoiceConfig      `json:"voice,omitempty"`
		Extensions    map[string]any    `json:"extensions,omitempty"`
		Metadata      map[string]any    `json:"metadata,omitempty"`
		Extra         map[string]any    `json:"-"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw.Model) == "" {
		return nil, strictErr("model", "model is required")
	}
	if len(raw.Messages) == 0 {
		return nil, strictErr("messages", "messages is required")
	}

	out := &MessageRequest{
		Model:         raw.Model,
		MaxTokens:     raw.MaxTokens,
		Temperature:   raw.Temperature,
		TopP:          raw.TopP,
		TopK:          raw.TopK,
		StopSequences: raw.StopSequences,
		ToolChoice:    raw.ToolChoice,
		Stream:        raw.Stream,
		OutputFormat:  raw.OutputFormat,
		Output:        raw.Output,
		Voice:         raw.Voice,
		Extensions:    raw.Extensions,
		Metadata:      raw.Metadata,
	}

	// system: string or []ContentBlock
	if !isNullOrEmptyJSON(raw.System) {
		var sysStr string
		if err := json.Unmarshal(raw.System, &sysStr); err == nil {
			out.System = sysStr
		} else {
			blocks, err := UnmarshalContentBlocksStrict(raw.System)
			if err != nil {
				if se, ok := err.(*StrictDecodeError); ok {
					if strings.HasPrefix(se.Param, "[") {
						se.Param = "system" + se.Param
					} else {
						se.Param = "system." + se.Param
					}
					return nil, se
				}
				return nil, strictErr("system", "system must be a string or an array of content blocks")
			}
			out.System = blocks
		}
	}

	// messages
	out.Messages = make([]Message, len(raw.Messages))
	for i, m := range raw.Messages {
		msg, err := unmarshalMessageStrict(m, fmt.Sprintf("messages[%d]", i))
		if err != nil {
			return nil, err
		}
		out.Messages[i] = msg
	}

	// tools
	if len(raw.Tools) > 0 {
		out.Tools = make([]Tool, len(raw.Tools))
		for i, t := range raw.Tools {
			tool, err := unmarshalToolStrict(t, fmt.Sprintf("tools[%d]", i))
			if err != nil {
				return nil, err
			}
			out.Tools[i] = tool
		}
	}

	if err := validateOutputFormatStrict(out.OutputFormat); err != nil {
		return nil, err
	}

	if err := validateToolHistoryStrict(out.Messages); err != nil {
		return nil, err
	}

	return out, nil
}

func validateContentBlockRoleStrict(role string, block ContentBlock, param string) error {
	switch block.(type) {
	case ThinkingBlock, *ThinkingBlock:
		if role != "assistant" {
			return strictErr(param, "thinking blocks are only allowed in assistant messages")
		}
	case ToolUseBlock, *ToolUseBlock:
		if role != "assistant" {
			return strictErr(param, "tool_use blocks are only allowed in assistant messages")
		}
	case ServerToolUseBlock, *ServerToolUseBlock:
		if role != "assistant" {
			return strictErr(param, "server_tool_use blocks are only allowed in assistant messages")
		}
	case ToolResultBlock, *ToolResultBlock:
		if role != "user" {
			return strictErr(param, "tool_result blocks are only allowed in user messages")
		}
	case WebSearchToolResultBlock, *WebSearchToolResultBlock:
		if role != "user" {
			return strictErr(param, "web_search_tool_result blocks are only allowed in user messages")
		}
	}
	return nil
}

func validateOutputFormatStrict(outputFormat *OutputFormat) error {
	if outputFormat == nil {
		return nil
	}

	formatType := strings.TrimSpace(outputFormat.Type)
	if formatType == "" {
		return strictErr("output_format.type", "output_format.type is required")
	}
	if formatType != "json_schema" {
		return strictErr("output_format.type", `output_format.type must be "json_schema"`)
	}
	if outputFormat.JSONSchema == nil {
		return strictErr("output_format.json_schema", `output_format.json_schema is required when output_format.type is "json_schema"`)
	}

	return validateJSONSchemaStrict(outputFormat.JSONSchema, "output_format.json_schema")
}

func validateJSONSchemaStrict(schema *JSONSchema, paramPrefix string) error {
	if schema == nil {
		return strictErr(paramPrefix, paramPrefix+" is required")
	}

	schemaType := strings.TrimSpace(schema.Type)
	if schemaType == "" {
		return strictErr(paramPrefix+".type", paramPrefix+".type is required")
	}
	if _, ok := allowedJSONSchemaTypes[schemaType]; !ok {
		return strictErr(paramPrefix+".type", fmt.Sprintf(`%s.type must be one of: object, array, string, number, integer, boolean, null`, paramPrefix))
	}

	switch schemaType {
	case "array":
		if schema.Items == nil {
			return strictErr(paramPrefix+".items", paramPrefix+`.items is required when `+paramPrefix+`.type is "array"`)
		}
		if err := validateJSONSchemaStrict(schema.Items, paramPrefix+".items"); err != nil {
			return err
		}

	case "object":
		for propertyName, propertySchema := range schema.Properties {
			prop := propertySchema
			if err := validateJSONSchemaStrict(&prop, paramPrefix+".properties."+propertyName); err != nil {
				return err
			}
		}
		for idx, requiredName := range schema.Required {
			trimmed := strings.TrimSpace(requiredName)
			if trimmed == "" {
				return strictErr(fmt.Sprintf("%s.required[%d]", paramPrefix, idx), "required property name must be non-empty")
			}
			if _, ok := schema.Properties[trimmed]; !ok {
				return strictErr(fmt.Sprintf("%s.required[%d]", paramPrefix, idx), fmt.Sprintf("%s.required[%d] references %q which is not defined in %s.properties", paramPrefix, idx, trimmed, paramPrefix))
			}
		}
	}

	return nil
}
