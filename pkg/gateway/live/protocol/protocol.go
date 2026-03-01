package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

const (
	ProtocolVersion1 = "1"

	AudioTransportBinary     = "binary"
	AudioTransportBase64JSON = "base64_json"

	VoiceProviderCartesia   = "cartesia"
	VoiceProviderElevenLabs = "elevenlabs"

	AlignmentKindChar = "char"
)

type DecodeError struct {
	Code    string
	Message string
	Param   string
}

func (e *DecodeError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Param) == "" {
		return e.Message
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Param)
}

func badRequest(message, param string) *DecodeError {
	return &DecodeError{Code: "bad_request", Message: message, Param: param}
}

func unsupported(message, param string) *DecodeError {
	return &DecodeError{Code: "unsupported", Message: message, Param: param}
}

// AudioFormat describes negotiated live audio shape.
type AudioFormat struct {
	Encoding     string `json:"encoding"`
	SampleRateHz int    `json:"sample_rate_hz"`
	Channels     int    `json:"channels"`
}

type HelloClient struct {
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	Platform string `json:"platform,omitempty"`
}

type HelloAuth struct {
	Mode          string `json:"mode,omitempty"`
	GatewayAPIKey string `json:"gateway_api_key,omitempty"`
}

type HelloBYOK struct {
	Anthropic  string            `json:"anthropic,omitempty"`
	OpenAI     string            `json:"openai,omitempty"`
	Gemini     string            `json:"gemini,omitempty"`
	Groq       string            `json:"groq,omitempty"`
	Cerebras   string            `json:"cerebras,omitempty"`
	OpenRouter string            `json:"openrouter,omitempty"`
	Cartesia   string            `json:"cartesia,omitempty"`
	ElevenLabs string            `json:"elevenlabs,omitempty"`
	Keys       map[string]string `json:"keys,omitempty"`
}

type HelloVoice struct {
	Provider string  `json:"provider,omitempty"`
	Language string  `json:"language,omitempty"`
	VoiceID  string  `json:"voice_id,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
	Volume   float64 `json:"volume,omitempty"`
	Emotion  string  `json:"emotion,omitempty"`
}

type HelloFeatures struct {
	AudioTransport         string `json:"audio_transport,omitempty"`
	SendPlaybackMarks      bool   `json:"send_playback_marks,omitempty"`
	WantPartialTranscripts bool   `json:"want_partial_transcripts,omitempty"`
	WantAssistantText      bool   `json:"want_assistant_text,omitempty"`
	ClientHasAEC           bool   `json:"client_has_aec,omitempty"`
	WantRunEvents          bool   `json:"want_run_events,omitempty"`
}

type HelloTools struct {
	ServerTools      []string       `json:"server_tools,omitempty"`
	ServerToolConfig map[string]any `json:"server_tool_config,omitempty"`
	ClientTools      []types.Tool   `json:"client_tools,omitempty"`
}

type ClientHello struct {
	Type               string        `json:"type"`
	ProtocolVersion    string        `json:"protocol_version"`
	Client             HelloClient   `json:"client,omitempty"`
	Auth               *HelloAuth    `json:"auth,omitempty"`
	Model              string        `json:"model"`
	System             string        `json:"system,omitempty"`
	Messages           []types.Message `json:"messages,omitempty"`
	BYOK               HelloBYOK     `json:"byok,omitempty"`
	AudioIn            AudioFormat   `json:"audio_in"`
	AudioOut           AudioFormat   `json:"audio_out"`
	Voice              *HelloVoice   `json:"voice,omitempty"`
	Tools              *HelloTools   `json:"tools,omitempty"`
	Features           HelloFeatures `json:"features,omitempty"`
	ResumeSessionID    string        `json:"resume_session_id,omitempty"`
	LastClientAudioSeq *int64        `json:"last_client_audio_seq,omitempty"`
}

func (h ClientHello) RedactedForLog() map[string]any {
	byokKeyNames := make([]string, 0, len(h.BYOK.Keys))
	for k := range h.BYOK.Keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		byokKeyNames = append(byokKeyNames, k)
	}
	sort.Strings(byokKeyNames)
	if len(byokKeyNames) > 32 {
		byokKeyNames = byokKeyNames[:32]
	}
	serverTools := make([]string, 0)
	clientTools := make([]string, 0)
	if h.Tools != nil {
		for _, name := range h.Tools.ServerTools {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			serverTools = append(serverTools, name)
		}
		sort.Strings(serverTools)
		for _, tool := range h.Tools.ClientTools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			clientTools = append(clientTools, name)
		}
		sort.Strings(clientTools)
	}

	return map[string]any{
		"type":             h.Type,
		"protocol_version": h.ProtocolVersion,
		"model":            h.Model,
		"audio_in":         h.AudioIn,
		"audio_out":        h.AudioOut,
		"features":         h.Features,
		"has_gateway_key":  h.Auth != nil && strings.TrimSpace(h.Auth.GatewayAPIKey) != "",
		"has_byok": map[string]bool{
			"anthropic":  strings.TrimSpace(h.BYOK.Anthropic) != "",
			"openai":     strings.TrimSpace(h.BYOK.OpenAI) != "",
			"gemini":     strings.TrimSpace(h.BYOK.Gemini) != "",
			"groq":       strings.TrimSpace(h.BYOK.Groq) != "",
			"cerebras":   strings.TrimSpace(h.BYOK.Cerebras) != "",
			"openrouter": strings.TrimSpace(h.BYOK.OpenRouter) != "",
			"cartesia":   strings.TrimSpace(h.BYOK.Cartesia) != "",
			"elevenlabs": strings.TrimSpace(h.BYOK.ElevenLabs) != "",
		},
		"has_byok_keys":    len(h.BYOK.Keys) > 0,
		"byok_key_names":   byokKeyNames,
		"has_server_tools": h.Tools != nil && len(serverTools) > 0,
		"server_tools":     serverTools,
		"has_client_tools": h.Tools != nil && len(clientTools) > 0,
		"client_tools":     clientTools,
		"message_count":    len(h.Messages),
		"has_system":       strings.TrimSpace(h.System) != "",
	}
}

type ClientAudioFrame struct {
	Type        string `json:"type"`
	Seq         int64  `json:"seq,omitempty"`
	TimestampMS *int64 `json:"timestamp_ms,omitempty"`
	DataB64     string `json:"data_b64"`
}

type ClientAudioStreamStart struct {
	Type         string `json:"type"`
	StreamID     string `json:"stream_id"`
	Encoding     string `json:"encoding"`
	SampleRateHz int    `json:"sample_rate_hz"`
	Channels     int    `json:"channels"`
}

type ClientAudioStreamEnd struct {
	Type     string `json:"type"`
	StreamID string `json:"stream_id,omitempty"`
}

type ClientPlaybackMark struct {
	Type             string `json:"type"`
	AssistantAudioID string `json:"assistant_audio_id"`
	PlayedMS         int64  `json:"played_ms"`
	BufferedMS       int64  `json:"buffered_ms,omitempty"`
	State            string `json:"state,omitempty"`
	TimestampMS      *int64 `json:"timestamp_ms,omitempty"`
}

type ClientControl struct {
	Type string `json:"type"`
	Op   string `json:"op"`
}

type ClientToolResult struct {
	Type    string               `json:"type"`
	TurnID  int                  `json:"turn_id"`
	ID      string               `json:"id"`
	Content []types.ContentBlock `json:"content,omitempty"`
	IsError bool                 `json:"is_error,omitempty"`
	Error   *types.Error         `json:"error,omitempty"`
}

func DecodeClientMessage(data []byte) (any, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, badRequest("invalid json frame", "")
	}
	typ := strings.TrimSpace(envelope.Type)
	if typ == "" {
		return nil, badRequest("missing type", "type")
	}

	switch typ {
	case "hello":
		var msg ClientHello
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, badRequest("invalid hello frame", "")
		}
		if err := ValidateHello(msg); err != nil {
			return nil, err
		}
		return msg, nil
	case "audio_frame":
		var msg ClientAudioFrame
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, badRequest("invalid audio_frame", "")
		}
		if strings.TrimSpace(msg.DataB64) == "" {
			return nil, badRequest("audio_frame.data_b64 is required", "data_b64")
		}
		return msg, nil
	case "audio_stream_start":
		var msg ClientAudioStreamStart
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, badRequest("invalid audio_stream_start", "")
		}
		if strings.TrimSpace(msg.StreamID) == "" {
			return nil, badRequest("audio_stream_start.stream_id is required", "stream_id")
		}
		if strings.TrimSpace(msg.Encoding) == "" {
			return nil, badRequest("audio_stream_start.encoding is required", "encoding")
		}
		if msg.SampleRateHz <= 0 {
			return nil, badRequest("audio_stream_start.sample_rate_hz must be > 0", "sample_rate_hz")
		}
		if msg.Channels <= 0 {
			return nil, badRequest("audio_stream_start.channels must be > 0", "channels")
		}
		return msg, nil
	case "audio_stream_end":
		var msg ClientAudioStreamEnd
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, badRequest("invalid audio_stream_end", "")
		}
		return msg, nil
	case "playback_mark":
		var msg ClientPlaybackMark
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, badRequest("invalid playback_mark", "")
		}
		if strings.TrimSpace(msg.AssistantAudioID) == "" {
			return nil, badRequest("playback_mark.assistant_audio_id is required", "assistant_audio_id")
		}
		if msg.PlayedMS < 0 {
			return nil, badRequest("playback_mark.played_ms must be >= 0", "played_ms")
		}
		return msg, nil
	case "control":
		var msg ClientControl
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, badRequest("invalid control", "")
		}
		op := strings.TrimSpace(msg.Op)
		if op == "" {
			return nil, badRequest("control.op is required", "op")
		}
		switch op {
		case "interrupt", "cancel_turn", "end_session":
		default:
			return nil, unsupported("unsupported control operation", "op")
		}
		msg.Op = op
		return msg, nil
	case "tool_result":
		var raw struct {
			Type    string          `json:"type"`
			TurnID  int             `json:"turn_id"`
			ID      string          `json:"id"`
			Content json.RawMessage `json:"content,omitempty"`
			IsError bool            `json:"is_error,omitempty"`
			Error   *types.Error    `json:"error,omitempty"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, badRequest("invalid tool_result", "")
		}
		if raw.TurnID <= 0 {
			return nil, badRequest("tool_result.turn_id must be > 0", "turn_id")
		}
		if strings.TrimSpace(raw.ID) == "" {
			return nil, badRequest("tool_result.id is required", "id")
		}

		content := make([]types.ContentBlock, 0)
		if len(strings.TrimSpace(string(raw.Content))) > 0 && strings.TrimSpace(string(raw.Content)) != "null" {
			decoded, err := types.UnmarshalContentBlocks(raw.Content)
			if err != nil {
				return nil, badRequest("invalid tool_result.content", "content")
			}
			content = decoded
		}

		return ClientToolResult{
			Type:    raw.Type,
			TurnID:  raw.TurnID,
			ID:      strings.TrimSpace(raw.ID),
			Content: content,
			IsError: raw.IsError,
			Error:   raw.Error,
		}, nil
	default:
		return nil, badRequest("unsupported message type", "type")
	}
}

func ValidateHello(msg ClientHello) error {
	if strings.TrimSpace(msg.ProtocolVersion) == "" {
		return badRequest("hello.protocol_version is required", "protocol_version")
	}
	if strings.TrimSpace(msg.Model) == "" {
		return badRequest("hello.model is required", "model")
	}
	if strings.TrimSpace(msg.AudioIn.Encoding) == "" {
		return badRequest("hello.audio_in.encoding is required", "audio_in.encoding")
	}
	if msg.AudioIn.SampleRateHz <= 0 {
		return badRequest("hello.audio_in.sample_rate_hz must be > 0", "audio_in.sample_rate_hz")
	}
	if msg.AudioIn.Channels <= 0 {
		return badRequest("hello.audio_in.channels must be > 0", "audio_in.channels")
	}
	if strings.TrimSpace(msg.AudioOut.Encoding) == "" {
		return badRequest("hello.audio_out.encoding is required", "audio_out.encoding")
	}
	if msg.AudioOut.SampleRateHz <= 0 {
		return badRequest("hello.audio_out.sample_rate_hz must be > 0", "audio_out.sample_rate_hz")
	}
	if msg.AudioOut.Channels <= 0 {
		return badRequest("hello.audio_out.channels must be > 0", "audio_out.channels")
	}
	if err := validateHelloMessages(msg.Messages); err != nil {
		return err
	}
	if err := validateHelloTools(msg.Tools); err != nil {
		return err
	}
	if msg.Voice != nil {
		provider := strings.ToLower(strings.TrimSpace(msg.Voice.Provider))
		if provider == "" {
			return badRequest("hello.voice.provider is required", "voice.provider")
		}
		switch provider {
		case VoiceProviderCartesia, VoiceProviderElevenLabs:
		default:
			return unsupported("unsupported voice provider", "voice.provider")
		}
	}

	transport := strings.TrimSpace(msg.Features.AudioTransport)
	if transport == "" {
		msg.Features.AudioTransport = AudioTransportBase64JSON
		return nil
	}
	switch transport {
	case AudioTransportBinary, AudioTransportBase64JSON:
		return nil
	default:
		return unsupported("unsupported audio transport", "features.audio_transport")
	}
}

func validateHelloTools(tools *HelloTools) error {
	if tools == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(tools.ServerTools))
	for i, name := range tools.ServerTools {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return badRequest("hello.tools.server_tools entries must be non-empty", fmt.Sprintf("tools.server_tools[%d]", i))
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			return badRequest("hello.tools.server_tools entries must be unique", fmt.Sprintf("tools.server_tools[%d]", i))
		}
		seen[key] = struct{}{}
	}
	for name, raw := range tools.ServerToolConfig {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return badRequest("hello.tools.server_tool_config keys must be non-empty", "tools.server_tool_config")
		}
		if _, ok := seen[strings.ToLower(trimmed)]; !ok {
			return badRequest("hello.tools.server_tool_config must only include enabled server tools", "tools.server_tool_config."+trimmed)
		}
		obj, ok := raw.(map[string]any)
		if !ok || obj == nil {
			return badRequest("hello.tools.server_tool_config entries must be objects", "tools.server_tool_config."+trimmed)
		}
	}

	reserved := map[string]struct{}{
		"talk_to_user":   {},
		"vai_web_search": {},
		"vai_web_fetch":  {},
	}
	for i, tool := range tools.ClientTools {
		if strings.TrimSpace(tool.Type) != types.ToolTypeFunction {
			return badRequest("hello.tools.client_tools entries must be function tools", fmt.Sprintf("tools.client_tools[%d].type", i))
		}
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return badRequest("hello.tools.client_tools entries must have a name", fmt.Sprintf("tools.client_tools[%d].name", i))
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return badRequest("hello.tools.client_tools name collides with another tool", fmt.Sprintf("tools.client_tools[%d].name", i))
		}
		if _, blocked := reserved[key]; blocked {
			return badRequest("hello.tools.client_tools contains a reserved tool name", fmt.Sprintf("tools.client_tools[%d].name", i))
		}
		if tool.InputSchema == nil {
			return badRequest("hello.tools.client_tools entries must include input_schema", fmt.Sprintf("tools.client_tools[%d].input_schema", i))
		}
		seen[key] = struct{}{}
	}

	return nil
}

func validateHelloMessages(messages []types.Message) error {
	for i, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "" {
			return badRequest("hello.messages entries must include role", fmt.Sprintf("messages[%d].role", i))
		}
		switch role {
		case "user", "assistant":
		default:
			return badRequest("hello.messages role must be user or assistant", fmt.Sprintf("messages[%d].role", i))
		}
		if message.Content == nil {
			return badRequest("hello.messages entries must include content", fmt.Sprintf("messages[%d].content", i))
		}
	}
	return nil
}

type HelloAckFeatures struct {
	AudioTransport    string `json:"audio_transport"`
	SupportsAlignment bool   `json:"supports_alignment"`
	AlignmentKind     string `json:"alignment_kind,omitempty"`
}

type HelloAckResume struct {
	Supported bool   `json:"supported"`
	Accepted  bool   `json:"accepted"`
	Reason    string `json:"reason,omitempty"`
}

type HelloAckLimits struct {
	MaxAudioFrameBytes  int   `json:"max_audio_frame_bytes"`
	MaxJSONMessageBytes int   `json:"max_json_message_bytes"`
	MaxAudioFPS         int   `json:"max_audio_fps,omitempty"`
	MaxAudioBPS         int64 `json:"max_audio_bps,omitempty"`
	InboundBurstSeconds int   `json:"inbound_burst_seconds,omitempty"`
	SilenceCommitMS     int   `json:"silence_commit_ms"`
	GraceMS             int   `json:"grace_ms"`
	RunTimeoutMS        int   `json:"run_timeout_ms,omitempty"`
}

type ServerHelloAck struct {
	Type            string           `json:"type"`
	ProtocolVersion string           `json:"protocol_version"`
	SessionID       string           `json:"session_id"`
	AudioIn         AudioFormat      `json:"audio_in"`
	AudioOut        AudioFormat      `json:"audio_out"`
	Features        HelloAckFeatures `json:"features"`
	Resume          HelloAckResume   `json:"resume"`
	Limits          *HelloAckLimits  `json:"limits,omitempty"`
}

type ServerError struct {
	Type      string         `json:"type"`
	Scope     string         `json:"scope,omitempty"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable,omitempty"`
	Close     bool           `json:"close,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

type ServerWarning struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ServerAudioInAck struct {
	Type        string `json:"type"`
	StreamID    string `json:"stream_id"`
	LastSeq     int64  `json:"last_seq"`
	TimestampMS int64  `json:"timestamp_ms,omitempty"`
}

type ServerTranscriptDelta struct {
	Type        string   `json:"type"`
	UtteranceID string   `json:"utterance_id"`
	IsFinal     bool     `json:"is_final"`
	Text        string   `json:"text"`
	Stability   *float64 `json:"stability,omitempty"`
	TimestampMS int64    `json:"timestamp_ms,omitempty"`
}

type ServerUtteranceFinal struct {
	Type        string `json:"type"`
	UtteranceID string `json:"utterance_id"`
	Text        string `json:"text"`
	EndMS       int64  `json:"end_ms"`
}

type ServerAssistantAudioStart struct {
	Type             string      `json:"type"`
	AssistantAudioID string      `json:"assistant_audio_id"`
	Format           AudioFormat `json:"format"`
	Text             string      `json:"text,omitempty"`
}

type ServerAssistantAudioChunk struct {
	Type             string     `json:"type"`
	AssistantAudioID string     `json:"assistant_audio_id"`
	Seq              int64      `json:"seq"`
	AudioB64         string     `json:"audio_b64,omitempty"`
	Alignment        *Alignment `json:"alignment,omitempty"`
}

type ServerAssistantAudioChunkHeader struct {
	Type             string     `json:"type"`
	AssistantAudioID string     `json:"assistant_audio_id"`
	Seq              int64      `json:"seq"`
	Bytes            int        `json:"bytes"`
	Alignment        *Alignment `json:"alignment,omitempty"`
}

type Alignment struct {
	Kind        string   `json:"kind"`
	Normalized  bool     `json:"normalized"`
	Chars       []string `json:"chars,omitempty"`
	CharStartMS []int    `json:"char_start_ms,omitempty"`
	CharDurMS   []int    `json:"char_dur_ms,omitempty"`
}

type ServerAssistantAudioEnd struct {
	Type             string `json:"type"`
	AssistantAudioID string `json:"assistant_audio_id"`
}

// ServerAssistantTextDelta streams append-only caption text for an assistant audio segment.
// Clients should append Delta to the accumulated caption for the given AssistantAudioID.
type ServerAssistantTextDelta struct {
	Type             string `json:"type"`
	AssistantAudioID string `json:"assistant_audio_id"`
	Delta            string `json:"delta"`
}

// ServerAssistantTextFinal provides the authoritative final caption text for an assistant audio segment.
// It must equal the concatenation of all prior ServerAssistantTextDelta.Delta values.
type ServerAssistantTextFinal struct {
	Type             string `json:"type"`
	AssistantAudioID string `json:"assistant_audio_id"`
	Text             string `json:"text"`
}

type ServerAudioReset struct {
	Type             string `json:"type"`
	Reason           string `json:"reason"`
	AssistantAudioID string `json:"assistant_audio_id,omitempty"`
}

type ServerRunEvent struct {
	Type   string          `json:"type"`
	TurnID int             `json:"turn_id"`
	Event  json.RawMessage `json:"event"`
}

type ServerToolCall struct {
	Type   string         `json:"type"`
	TurnID int            `json:"turn_id"`
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Input  map[string]any `json:"input,omitempty"`
}

type ServerToolCancel struct {
	Type   string `json:"type"`
	TurnID int    `json:"turn_id"`
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}
