package types

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalMessageRequestStrict_SystemUnion(t *testing.T) {
	t.Run("string ok", func(t *testing.T) {
		req, err := UnmarshalMessageRequestStrict([]byte(`{
			"model":"anthropic/claude",
			"system":"hi",
			"messages":[{"role":"user","content":"hello"}]
		}`))
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if _, ok := req.System.(string); !ok {
			t.Fatalf("expected system to be string, got %T", req.System)
		}
	})

	t.Run("blocks ok", func(t *testing.T) {
		req, err := UnmarshalMessageRequestStrict([]byte(`{
			"model":"anthropic/claude",
			"system":[{"type":"text","text":"hi"}],
			"messages":[{"role":"user","content":"hello"}]
		}`))
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if _, ok := req.System.([]ContentBlock); !ok {
			t.Fatalf("expected system to be []ContentBlock, got %T", req.System)
		}
	})

	t.Run("object rejected", func(t *testing.T) {
		_, err := UnmarshalMessageRequestStrict([]byte(`{
			"model":"anthropic/claude",
			"system":{"bad":true},
			"messages":[{"role":"user","content":"hello"}]
		}`))
		if err == nil {
			t.Fatalf("expected error")
		}
		if se, ok := err.(*StrictDecodeError); !ok || se.Param != "system" {
			t.Fatalf("expected StrictDecodeError param=system, got %T %v", err, err)
		}
	})
}

func TestUnmarshalMessageRequestStrict_UnknownBlockRejected(t *testing.T) {
	_, err := UnmarshalMessageRequestStrict([]byte(`{
		"model":"anthropic/claude",
		"messages":[{"role":"user","content":[{"type":"made_up","x":1}]}]
	}`))
	if err == nil {
		t.Fatalf("expected error")
	}
	se, ok := err.(*StrictDecodeError)
	if !ok {
		t.Fatalf("expected StrictDecodeError, got %T", err)
	}
	if se.Param == "" {
		t.Fatalf("expected param, got empty: %v", se)
	}
}

func TestUnmarshalMessageRequestStrict_ToolConfigTyping(t *testing.T) {
	req, err := UnmarshalMessageRequestStrict([]byte(`{
		"model":"anthropic/claude",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"web_search","config":{"max_uses":2}}]
	}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if _, ok := req.Tools[0].Config.(*WebSearchConfig); !ok {
		t.Fatalf("expected tool config to be *WebSearchConfig, got %T", req.Tools[0].Config)
	}
}

func TestUnmarshalMessageRequestStrict_FunctionToolConfigRejected(t *testing.T) {
	_, err := UnmarshalMessageRequestStrict([]byte(`{
		"model":"anthropic/claude",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","name":"t","input_schema":{"type":"object"},"config":{"x":1}}]
	}`))
	if err == nil {
		t.Fatalf("expected error")
	}
	se, ok := err.(*StrictDecodeError)
	if !ok {
		t.Fatalf("expected StrictDecodeError, got %T", err)
	}
	if se.Param != "tools[0].config" {
		t.Fatalf("param=%q, expected tools[0].config", se.Param)
	}
}

func TestUnmarshalMessageRequestStrict_ToolHistoryValidation(t *testing.T) {
	t.Run("tool_result must match prior tool_use", func(t *testing.T) {
		_, err := UnmarshalMessageRequestStrict([]byte(`{
			"model":"anthropic/claude",
			"messages":[
				{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"t","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_2","content":[{"type":"text","text":"x"}]}]}
			]
		}`))
		if err == nil {
			t.Fatalf("expected error")
		}
		se, ok := err.(*StrictDecodeError)
		if !ok {
			t.Fatalf("expected StrictDecodeError, got %T", err)
		}
		if se.Param == "" {
			t.Fatalf("expected param")
		}
	})

	t.Run("valid tool history ok", func(t *testing.T) {
		req, err := UnmarshalMessageRequestStrict([]byte(`{
			"model":"anthropic/claude",
			"messages":[
				{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"t","input":{"a":1}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"ok"}]}]}
			]
		}`))
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		// Ensure tool_use input decoded as object (map).
		blocks := req.Messages[0].ContentBlocks()
		tu, ok := blocks[0].(ToolUseBlock)
		if !ok {
			t.Fatalf("expected ToolUseBlock, got %T", blocks[0])
		}
		b, _ := json.Marshal(tu.Input)
		if string(b) != `{"a":1}` {
			t.Fatalf("unexpected input: %s", string(b))
		}
	})
}
