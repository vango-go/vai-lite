package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeClientMessage_Hello(t *testing.T) {
	raw := []byte(`{
		"type":"hello",
		"protocol_version":"1",
		"model":"anthropic/claude-sonnet-4",
		"audio_in":{"encoding":"pcm_s16le","sample_rate_hz":16000,"channels":1},
		"audio_out":{"encoding":"pcm_s16le","sample_rate_hz":24000,"channels":1},
		"features":{"audio_transport":"binary"}
	}`)

	msg, err := DecodeClientMessage(raw)
	if err != nil {
		t.Fatalf("DecodeClientMessage() error = %v", err)
	}
	hello, ok := msg.(ClientHello)
	if !ok {
		t.Fatalf("decoded type = %T, want ClientHello", msg)
	}
	if hello.ProtocolVersion != "1" {
		t.Fatalf("protocol_version=%q", hello.ProtocolVersion)
	}
}

func TestDecodeClientMessage_HelloWithTools(t *testing.T) {
	raw := []byte(`{
		"type":"hello",
		"protocol_version":"1",
		"model":"anthropic/claude-sonnet-4",
		"audio_in":{"encoding":"pcm_s16le","sample_rate_hz":16000,"channels":1},
		"audio_out":{"encoding":"pcm_s16le","sample_rate_hz":24000,"channels":1},
		"tools":{
			"server_tools":["vai_web_search"],
			"server_tool_config":{"vai_web_search":{"provider":"tavily"}}
		}
	}`)

	msg, err := DecodeClientMessage(raw)
	if err != nil {
		t.Fatalf("DecodeClientMessage() error = %v", err)
	}
	hello := msg.(ClientHello)
	if hello.Tools == nil || len(hello.Tools.ServerTools) != 1 {
		t.Fatalf("tools=%+v", hello.Tools)
	}
}

func TestValidateHello_RejectsToolConfigForDisabledTool(t *testing.T) {
	err := ValidateHello(ClientHello{
		Type:            "hello",
		ProtocolVersion: "1",
		Model:           "anthropic/claude-sonnet-4",
		AudioIn:         AudioFormat{Encoding: "pcm_s16le", SampleRateHz: 16000, Channels: 1},
		AudioOut:        AudioFormat{Encoding: "pcm_s16le", SampleRateHz: 24000, Channels: 1},
		Tools: &HelloTools{
			ServerTools:      []string{"vai_web_search"},
			ServerToolConfig: map[string]any{"vai_web_fetch": map[string]any{"provider": "firecrawl"}},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDecodeClientMessage_HelloMissingRequired(t *testing.T) {
	raw := []byte(`{"type":"hello","protocol_version":"1"}`)
	_, err := DecodeClientMessage(raw)
	if err == nil {
		t.Fatalf("expected error")
	}
	decErr, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("err type = %T", err)
	}
	if decErr.Code != "bad_request" {
		t.Fatalf("code=%q", decErr.Code)
	}
}

func TestDecodeClientMessage_UnsupportedControlOp(t *testing.T) {
	raw := []byte(`{"type":"control","op":"reboot"}`)
	_, err := DecodeClientMessage(raw)
	if err == nil {
		t.Fatalf("expected error")
	}
	decErr, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("err type = %T", err)
	}
	if decErr.Code != "unsupported" {
		t.Fatalf("code=%q", decErr.Code)
	}
}

func TestClientHelloRedaction(t *testing.T) {
	h := ClientHello{
		Type:            "hello",
		ProtocolVersion: "1",
		Model:           "anthropic/claude-sonnet-4",
		BYOK: HelloBYOK{
			Anthropic:  "sk-ant-secret",
			Cartesia:   "sk-car-secret",
			Groq:       "sk-groq-secret",
			Cerebras:   "sk-cerebras-secret",
			OpenRouter: "sk-openrouter-secret",
			Keys: map[string]string{
				"anthropic": "sk-ant-secret-2",
				"openai":    "sk-openai-secret",
			},
		},
		Auth:     &HelloAuth{GatewayAPIKey: "vai_sk_secret"},
		AudioIn:  AudioFormat{Encoding: "pcm_s16le", SampleRateHz: 16000, Channels: 1},
		AudioOut: AudioFormat{Encoding: "pcm_s16le", SampleRateHz: 24000, Channels: 1},
		Tools: &HelloTools{
			ServerTools:      []string{"vai_web_search"},
			ServerToolConfig: map[string]any{"vai_web_search": map[string]any{"provider": "tavily"}},
		},
	}

	redacted := h.RedactedForLog()
	blob, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(blob) == "" {
		t.Fatalf("empty redacted payload")
	}
	if strings.Contains(string(blob), "secret") {
		t.Fatalf("redacted payload leaked secret: %s", string(blob))
	}
	if !strings.Contains(string(blob), "has_byok_keys") {
		t.Fatalf("expected has_byok_keys in redacted payload: %s", string(blob))
	}
	if strings.Contains(string(blob), "vai_web_search") == false {
		t.Fatalf("expected server tool names in redacted payload: %s", string(blob))
	}
}
