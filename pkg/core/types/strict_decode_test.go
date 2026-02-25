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

func TestUnmarshalMessageRequestStrict_RoleConstraints(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name: "thinking in user rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":[{"type":"thinking","thinking":"x"}]}]
			}`,
			wantParam: "messages[0].content[0].type",
		},
		{
			name: "tool_use in user rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":[{"type":"tool_use","id":"call_1","name":"t","input":{"q":"x"}}]}]
			}`,
			wantParam: "messages[0].content[0].type",
		},
		{
			name: "tool_result in assistant rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"assistant","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"ok"}]}]}]
			}`,
			wantParam: "messages[0].content[0].type",
		},
		{
			name: "server_tool_use in user rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":[{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"x"}}]}]
			}`,
			wantParam: "messages[0].content[0].type",
		},
		{
			name: "web_search_tool_result in assistant rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"assistant","content":[{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[]}]}]
			}`,
			wantParam: "messages[0].content[0].type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UnmarshalMessageRequestStrict([]byte(tt.payload))
			se := requireStrictDecodeError(t, err)
			if se.Param != tt.wantParam {
				t.Fatalf("param=%q, want %q", se.Param, tt.wantParam)
			}
		})
	}
}

func TestUnmarshalMessageRequestStrict_RoleEnumEnforced(t *testing.T) {
	_, err := UnmarshalMessageRequestStrict([]byte(`{
		"model":"anthropic/claude",
		"messages":[{"role":"system","content":"hello"}]
	}`))
	se := requireStrictDecodeError(t, err)
	if se.Param != "messages[0].role" {
		t.Fatalf("param=%q, want messages[0].role", se.Param)
	}
}

func TestUnmarshalMessageRequestStrict_RoleConstraint_ValidPlacements(t *testing.T) {
	_, err := UnmarshalMessageRequestStrict([]byte(`{
		"model":"anthropic/claude",
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"let me check"},
				{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"latest"}},
				{"type":"tool_use","id":"call_1","name":"my_tool","input":{"x":1}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"done"}]},
				{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[]}
			]}
		]
	}`))
	if err != nil {
		t.Fatalf("expected valid placements to pass, got err=%v", err)
	}
}

func TestUnmarshalMessageRequestStrict_FunctionToolDescriptionRequired(t *testing.T) {
	_, err := UnmarshalMessageRequestStrict([]byte(`{
		"model":"anthropic/claude",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","name":"lookup","input_schema":{"type":"object"}}]
	}`))
	se := requireStrictDecodeError(t, err)
	if se.Param != "tools[0].description" {
		t.Fatalf("param=%q, want tools[0].description", se.Param)
	}
}

func TestUnmarshalMessageRequestStrict_FunctionToolDescriptionPresent(t *testing.T) {
	_, err := UnmarshalMessageRequestStrict([]byte(`{
		"model":"anthropic/claude",
		"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","name":"lookup","description":"Lookup data","input_schema":{"type":"object"}}]
	}`))
	if err != nil {
		t.Fatalf("expected valid function tool to pass, got err=%v", err)
	}
}

func TestUnmarshalMessageRequestStrict_OutputFormatSemanticValidation(t *testing.T) {
	t.Run("valid structured output passes", func(t *testing.T) {
		_, err := UnmarshalMessageRequestStrict([]byte(`{
			"model":"anthropic/claude",
			"messages":[{"role":"user","content":"extract"}],
			"output_format":{
				"type":"json_schema",
				"json_schema":{
					"type":"object",
					"properties":{
						"name":{"type":"string"},
						"items":{"type":"array","items":{"type":"integer"}}
					},
					"required":["name"]
				}
			}
		}`))
		if err != nil {
			t.Fatalf("expected valid output_format to pass, got err=%v", err)
		}
	})

	tests := []struct {
		name      string
		payload   string
		wantParam string
	}{
		{
			name: "missing output_format.type rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":"extract"}],
				"output_format":{"json_schema":{"type":"object"}}
			}`,
			wantParam: "output_format.type",
		},
		{
			name: "unsupported output_format.type rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":"extract"}],
				"output_format":{"type":"xml_schema","json_schema":{"type":"object"}}
			}`,
			wantParam: "output_format.type",
		},
		{
			name: "missing output_format.json_schema rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":"extract"}],
				"output_format":{"type":"json_schema"}
			}`,
			wantParam: "output_format.json_schema",
		},
		{
			name: "schema node missing type rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":"extract"}],
				"output_format":{"type":"json_schema","json_schema":{}}
			}`,
			wantParam: "output_format.json_schema.type",
		},
		{
			name: "array schema without items rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":"extract"}],
				"output_format":{"type":"json_schema","json_schema":{"type":"array"}}
			}`,
			wantParam: "output_format.json_schema.items",
		},
		{
			name: "required key not in properties rejected",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":"extract"}],
				"output_format":{
					"type":"json_schema",
					"json_schema":{
						"type":"object",
						"properties":{"name":{"type":"string"}},
						"required":["name","age"]
					}
				}
			}`,
			wantParam: "output_format.json_schema.required[1]",
		},
		{
			name: "nested schema path is precise",
			payload: `{
				"model":"anthropic/claude",
				"messages":[{"role":"user","content":"extract"}],
				"output_format":{
					"type":"json_schema",
					"json_schema":{
						"type":"object",
						"properties":{
							"data":{"type":"array","items":{"properties":{"name":{"type":"string"}}}}
						}
					}
				}
			}`,
			wantParam: "output_format.json_schema.properties.data.items.type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UnmarshalMessageRequestStrict([]byte(tt.payload))
			se := requireStrictDecodeError(t, err)
			if se.Param != tt.wantParam {
				t.Fatalf("param=%q, want %q", se.Param, tt.wantParam)
			}
		})
	}
}

func requireStrictDecodeError(t *testing.T, err error) *StrictDecodeError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected StrictDecodeError, got nil")
	}
	se, ok := err.(*StrictDecodeError)
	if !ok {
		t.Fatalf("expected StrictDecodeError, got %T (%v)", err, err)
	}
	return se
}
