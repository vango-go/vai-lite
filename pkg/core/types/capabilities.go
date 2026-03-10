package types

import "strings"

type ModelCapability struct {
	ID                        string   `json:"id"`
	Provider                  string   `json:"provider"`
	InputModalities           []string `json:"input_modalities,omitempty"`
	OutputModalities          []string `json:"output_modalities,omitempty"`
	SupportsFunctionTools     bool     `json:"supports_function_tools"`
	SupportsGatewayTools      bool     `json:"supports_gateway_tools"`
	SupportsStructuredOutput  bool     `json:"supports_structured_output"`
	SupportsReasoningControls bool     `json:"supports_reasoning_controls"`
	SupportsUpstreamWebSocket bool     `json:"supports_upstream_websocket"`
	SupportsContinuation      bool     `json:"supports_provider_continuation"`
	SupportsVision            bool     `json:"supports_vision"`
	SupportsDocuments         bool     `json:"supports_documents"`
	SupportsAudioInput        bool     `json:"supports_audio_input"`
	SupportsAudioOutput       bool     `json:"supports_audio_output"`
	MaxContextWindow          int      `json:"max_context_window,omitempty"`
}

var providerCapabilities = map[string]ModelCapability{
	"anthropic": {
		Provider:                  "anthropic",
		InputModalities:           []string{"text", "image", "document"},
		OutputModalities:          []string{"text"},
		SupportsFunctionTools:     true,
		SupportsGatewayTools:      true,
		SupportsStructuredOutput:  true,
		SupportsReasoningControls: true,
		SupportsVision:            true,
		SupportsDocuments:         true,
		MaxContextWindow:          200000,
	},
	"openai": {
		Provider:                 "openai",
		InputModalities:          []string{"text", "image"},
		OutputModalities:         []string{"text"},
		SupportsFunctionTools:    true,
		SupportsGatewayTools:     true,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		MaxContextWindow:         128000,
	},
	"oai-resp": {
		Provider:                  "oai-resp",
		InputModalities:           []string{"text", "image", "document"},
		OutputModalities:          []string{"text", "audio"},
		SupportsFunctionTools:     true,
		SupportsGatewayTools:      true,
		SupportsStructuredOutput:  true,
		SupportsReasoningControls: true,
		SupportsUpstreamWebSocket: true,
		SupportsContinuation:      true,
		SupportsVision:            true,
		SupportsDocuments:         true,
		SupportsAudioOutput:       true,
		MaxContextWindow:          200000,
	},
	"gem-dev": {
		Provider:                  "gem-dev",
		InputModalities:           []string{"text", "image", "document", "audio"},
		OutputModalities:          []string{"text", "image"},
		SupportsFunctionTools:     true,
		SupportsGatewayTools:      true,
		SupportsStructuredOutput:  true,
		SupportsReasoningControls: true,
		SupportsVision:            true,
		SupportsDocuments:         true,
		SupportsAudioInput:        true,
		MaxContextWindow:          1048576,
	},
	"gem-vert": {
		Provider:                  "gem-vert",
		InputModalities:           []string{"text", "image", "document", "audio"},
		OutputModalities:          []string{"text", "image"},
		SupportsFunctionTools:     true,
		SupportsGatewayTools:      true,
		SupportsStructuredOutput:  true,
		SupportsReasoningControls: true,
		SupportsVision:            true,
		SupportsDocuments:         true,
		SupportsAudioInput:        true,
		MaxContextWindow:          1048576,
	},
	"groq": {
		Provider:                 "groq",
		InputModalities:          []string{"text", "image"},
		OutputModalities:         []string{"text"},
		SupportsFunctionTools:    true,
		SupportsGatewayTools:     true,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		MaxContextWindow:         128000,
	},
	"cerebras": {
		Provider:                 "cerebras",
		InputModalities:          []string{"text"},
		OutputModalities:         []string{"text"},
		SupportsFunctionTools:    true,
		SupportsGatewayTools:     true,
		SupportsStructuredOutput: true,
		MaxContextWindow:         128000,
	},
	"openrouter": {
		Provider:                 "openrouter",
		InputModalities:          []string{"text", "image"},
		OutputModalities:         []string{"text"},
		SupportsFunctionTools:    true,
		SupportsGatewayTools:     true,
		SupportsStructuredOutput: true,
		SupportsVision:           true,
		MaxContextWindow:         128000,
	},
}

var modelCapabilities = map[string]ModelCapability{
	"anthropic/claude-sonnet-4":       providerCapabilities["anthropic"],
	"anthropic/claude-haiku-4-5":      providerCapabilities["anthropic"],
	"openai/gpt-4o":                   providerCapabilities["openai"],
	"openai/gpt-4o-mini":              providerCapabilities["openai"],
	"oai-resp/gpt-5":                  providerCapabilities["oai-resp"],
	"oai-resp/gpt-5-mini":             providerCapabilities["oai-resp"],
	"gem-dev/gemini-2.5-pro":          providerCapabilities["gem-dev"],
	"gem-dev/gemini-2.5-flash":        providerCapabilities["gem-dev"],
	"gem-vert/gemini-2.5-pro":         providerCapabilities["gem-vert"],
	"gem-vert/gemini-2.5-flash":       providerCapabilities["gem-vert"],
	"gem-vert/gemini-3-flash-preview": providerCapabilities["gem-vert"],
	"groq/llama-3.3-70b-versatile":    providerCapabilities["groq"],
	"cerebras/llama-3.3-70b":          providerCapabilities["cerebras"],
	"openrouter/openai/gpt-4o":        providerCapabilities["openrouter"],
}

func init() {
	for id, capability := range modelCapabilities {
		capability.ID = id
		if capability.Provider == "" {
			if provider, _, ok := splitCanonicalModelID(id); ok {
				capability.Provider = provider
			}
		}
		modelCapabilities[id] = capability
	}
}

func CapabilityRegistry() []ModelCapability {
	out := make([]ModelCapability, 0, len(modelCapabilities))
	for _, capability := range modelCapabilities {
		out = append(out, capability)
	}
	return out
}

func LookupCapability(model string) (ModelCapability, bool) {
	model = strings.TrimSpace(model)
	if capability, ok := modelCapabilities[model]; ok {
		return capability, true
	}
	provider, _, ok := splitCanonicalModelID(model)
	if !ok {
		return ModelCapability{}, false
	}
	capability, ok := providerCapabilities[provider]
	if !ok {
		return ModelCapability{}, false
	}
	capability.ID = model
	return capability, true
}

func ValidateCapability(model string, messages []Message, defaults ChainDefaults) *CanonicalError {
	capability, ok := LookupCapability(model)
	if !ok {
		return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "requested model is not supported by the capability registry")
	}

	if len(defaults.Tools) > 0 && !capability.SupportsFunctionTools {
		return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "function tools are not supported by the selected model")
	}
	if len(defaults.GatewayTools) > 0 && !capability.SupportsGatewayTools {
		return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "gateway tools are not supported by the selected model")
	}
	if defaults.OutputFormat != nil && !capability.SupportsStructuredOutput {
		return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "structured output is not supported by the selected model")
	}
	if defaults.Voice != nil && defaults.Voice.Output != nil && !capability.SupportsAudioOutput {
		return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "audio output is not supported by the selected model")
	}
	for _, msg := range messages {
		for _, block := range msg.ContentBlocks() {
			switch contentBlockType(block) {
			case "image":
				if !capability.SupportsVision {
					return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "image input is not supported by the selected model")
				}
			case "document":
				if !capability.SupportsDocuments {
					return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "document input is not supported by the selected model")
				}
			case "audio", "audio_stt":
				if !capability.SupportsAudioInput {
					return NewCanonicalError(ErrorCodeProtocolUnsupportedCapability, "audio input is not supported by the selected model")
				}
			}
		}
	}
	return nil
}

func splitCanonicalModelID(model string) (provider string, name string, ok bool) {
	model = strings.TrimSpace(model)
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func contentBlockType(block ContentBlock) string {
	switch b := block.(type) {
	case TextBlock:
		return b.Type
	case *TextBlock:
		if b != nil {
			return b.Type
		}
	case ImageBlock:
		return b.Type
	case *ImageBlock:
		if b != nil {
			return b.Type
		}
	case AudioBlock:
		return b.Type
	case *AudioBlock:
		if b != nil {
			return b.Type
		}
	case AudioSTTBlock:
		return b.Type
	case *AudioSTTBlock:
		if b != nil {
			return b.Type
		}
	case VideoBlock:
		return b.Type
	case *VideoBlock:
		if b != nil {
			return b.Type
		}
	case DocumentBlock:
		return b.Type
	case *DocumentBlock:
		if b != nil {
			return b.Type
		}
	case ToolUseBlock:
		return b.Type
	case *ToolUseBlock:
		if b != nil {
			return b.Type
		}
	case ToolResultBlock:
		return b.Type
	case *ToolResultBlock:
		if b != nil {
			return b.Type
		}
	case ThinkingBlock:
		return b.Type
	case *ThinkingBlock:
		if b != nil {
			return b.Type
		}
	case ServerToolUseBlock:
		return b.Type
	case *ServerToolUseBlock:
		if b != nil {
			return b.Type
		}
	case WebSearchToolResultBlock:
		return b.Type
	case *WebSearchToolResultBlock:
		if b != nil {
			return b.Type
		}
	}
	return ""
}
