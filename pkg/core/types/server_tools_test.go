package types

import (
	"strings"
	"testing"
)

func TestUnmarshalServerToolExecuteRequestStrict_Basic(t *testing.T) {
	req, err := UnmarshalServerToolExecuteRequestStrict([]byte(`{
		"tool":"vai_web_search",
		"input":{"query":"latest"},
		"server_tool_config":{"vai_web_search":{"provider":"tavily"}}
	}`))
	if err != nil {
		t.Fatalf("UnmarshalServerToolExecuteRequestStrict() error = %v", err)
	}
	if req.Tool != "vai_web_search" {
		t.Fatalf("tool=%q", req.Tool)
	}
	if req.Input["query"] != "latest" {
		t.Fatalf("input.query=%v", req.Input["query"])
	}
	if len(req.ServerToolConfig) != 1 {
		t.Fatalf("len(server_tool_config)=%d", len(req.ServerToolConfig))
	}
}

func TestUnmarshalServerToolExecuteRequestStrict_ExecutionContext(t *testing.T) {
	req, err := UnmarshalServerToolExecuteRequestStrict([]byte(`{
		"tool":"vai_image",
		"input":{"prompt":"edit it","images":[{"id":"img-01"}]},
		"execution_context":{
			"images":[
				{
					"id":"img-01",
					"image":{
						"type":"image",
						"source":{"type":"base64","media_type":"image/png","data":"Zm9v"}
					}
				}
			]
		}
	}`))
	if err != nil {
		t.Fatalf("UnmarshalServerToolExecuteRequestStrict() error = %v", err)
	}
	if req.ExecutionContext == nil {
		t.Fatal("expected execution_context")
	}
	if len(req.ExecutionContext.Images) != 1 {
		t.Fatalf("len(execution_context.images)=%d", len(req.ExecutionContext.Images))
	}
	if req.ExecutionContext.Images[0].ID != "img-01" {
		t.Fatalf("execution_context.images[0].id=%q", req.ExecutionContext.Images[0].ID)
	}
	if req.ExecutionContext.Images[0].Image.BlockType() != "image" {
		t.Fatalf("execution_context.images[0].image type=%q", req.ExecutionContext.Images[0].Image.BlockType())
	}
}

func TestUnmarshalServerToolExecuteRequestStrict_UnknownField(t *testing.T) {
	_, err := UnmarshalServerToolExecuteRequestStrict([]byte(`{"tool":"vai_web_search","input":{"query":"x"},"extra":true}`))
	if err == nil {
		t.Fatalf("expected error")
	}
	se, ok := err.(*StrictDecodeError)
	if !ok {
		t.Fatalf("error type=%T", err)
	}
	if se.Param != "extra" {
		t.Fatalf("param=%q", se.Param)
	}
}

func TestUnmarshalServerToolExecuteRequestStrict_InputValidation(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		param string
	}{
		{name: "missing tool", body: `{"input":{}}`, param: "tool"},
		{name: "empty tool", body: `{"tool":"  ","input":{}}`, param: "tool"},
		{name: "missing input", body: `{"tool":"vai_web_search"}`, param: "input"},
		{name: "input non-object", body: `{"tool":"vai_web_search","input":"nope"}`, param: "input"},
		{name: "config non-object", body: `{"tool":"vai_web_search","input":{},"server_tool_config":"nope"}`, param: "server_tool_config"},
		{name: "config entry non-object", body: `{"tool":"vai_web_search","input":{},"server_tool_config":{"vai_web_search":"nope"}}`, param: "server_tool_config.vai_web_search"},
		{name: "execution context unknown field", body: `{"tool":"vai_image","input":{"prompt":"x"},"execution_context":{"extra":true}}`, param: "execution_context.extra"},
		{name: "execution context images non-array", body: `{"tool":"vai_image","input":{"prompt":"x"},"execution_context":{"images":"nope"}}`, param: "execution_context.images"},
		{name: "execution context image missing id", body: `{"tool":"vai_image","input":{"prompt":"x"},"execution_context":{"images":[{"image":{"type":"image","source":{"type":"base64","media_type":"image/png","data":"Zm9v"}}}]}}`, param: "execution_context.images[0].id"},
		{name: "execution context image wrong type", body: `{"tool":"vai_image","input":{"prompt":"x"},"execution_context":{"images":[{"id":"img-01","image":{"type":"text","text":"oops"}}]}}`, param: "execution_context.images[0].image"},
		{name: "execution context duplicate ids", body: `{"tool":"vai_image","input":{"prompt":"x"},"execution_context":{"images":[{"id":"img-01","image":{"type":"image","source":{"type":"base64","media_type":"image/png","data":"Zm9v"}}},{"id":"img-01","image":{"type":"image","source":{"type":"base64","media_type":"image/png","data":"YmFy"}}}]}}`, param: "execution_context.images"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalServerToolExecuteRequestStrict([]byte(tc.body))
			if err == nil {
				t.Fatalf("expected error")
			}
			se, ok := err.(*StrictDecodeError)
			if !ok {
				t.Fatalf("error type=%T", err)
			}
			if se.Param != tc.param {
				t.Fatalf("param=%q, want %q", se.Param, tc.param)
			}
		})
	}
}

func TestUnmarshalServerToolExecuteResponse_DecodesContentBlocks(t *testing.T) {
	resp, err := UnmarshalServerToolExecuteResponse([]byte(`{
		"content":[{"type":"text","text":"ok"}]
	}`))
	if err != nil {
		t.Fatalf("UnmarshalServerToolExecuteResponse() error = %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("len(content)=%d", len(resp.Content))
	}
	if resp.Content[0].BlockType() != "text" {
		t.Fatalf("block type=%q", resp.Content[0].BlockType())
	}
}

func TestUnmarshalServerToolExecuteResponse_UnknownContentType(t *testing.T) {
	resp, err := UnmarshalServerToolExecuteResponse([]byte(`{"content":[{"type":"not_real","x":1}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("len(content)=%d", len(resp.Content))
	}
	if got := resp.Content[0].BlockType(); got != "not_real" {
		t.Fatalf("block type=%q", got)
	}
	if !strings.Contains(string(resp.Content[0].(UnknownContentBlock).Raw), `"x":1`) {
		t.Fatalf("raw block=%s", string(resp.Content[0].(UnknownContentBlock).Raw))
	}
}
