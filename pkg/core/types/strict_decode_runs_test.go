package types

import "testing"

func TestUnmarshalRunRequestStrict_Defaults(t *testing.T) {
	req, err := UnmarshalRunRequestStrict([]byte(`{
		"request": {
			"model": "anthropic/claude-sonnet-4",
			"messages": [{"role":"user","content":"hi"}]
		}
	}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if req.Run.MaxTurns != defaultRunMaxTurns {
		t.Fatalf("max_turns=%d", req.Run.MaxTurns)
	}
	if req.Run.MaxToolCalls != defaultRunMaxToolCalls {
		t.Fatalf("max_tool_calls=%d", req.Run.MaxToolCalls)
	}
	if req.Run.TimeoutMS != defaultRunTimeoutMS {
		t.Fatalf("timeout_ms=%d", req.Run.TimeoutMS)
	}
	if req.Run.ToolTimeoutMS != defaultRunToolTimeoutMS {
		t.Fatalf("tool_timeout_ms=%d", req.Run.ToolTimeoutMS)
	}
	if req.Run.ParallelTools != true {
		t.Fatalf("parallel_tools=%v", req.Run.ParallelTools)
	}
}

func TestUnmarshalRunRequestStrict_RequestStreamRejected(t *testing.T) {
	_, err := UnmarshalRunRequestStrict([]byte(`{
		"request": {
			"model": "anthropic/claude-sonnet-4",
			"stream": true,
			"messages": [{"role":"user","content":"hi"}]
		}
	}`))
	if err == nil {
		t.Fatal("expected error")
	}
	se, ok := err.(*StrictDecodeError)
	if !ok || se.Param != "request.stream" {
		t.Fatalf("err=%T %#v", err, err)
	}
}

func TestUnmarshalRunRequestStrict_RunBounds(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		param string
	}{
		{
			name:  "max_turns",
			body:  `{"request":{"model":"anthropic/x","messages":[{"role":"user","content":"hi"}]},"run":{"max_turns":0}}`,
			param: "run.max_turns",
		},
		{
			name:  "max_tool_calls",
			body:  `{"request":{"model":"anthropic/x","messages":[{"role":"user","content":"hi"}]},"run":{"max_tool_calls":999}}`,
			param: "run.max_tool_calls",
		},
		{
			name:  "timeout_ms",
			body:  `{"request":{"model":"anthropic/x","messages":[{"role":"user","content":"hi"}]},"run":{"timeout_ms":999}}`,
			param: "run.timeout_ms",
		},
		{
			name:  "tool_timeout_ms",
			body:  `{"request":{"model":"anthropic/x","messages":[{"role":"user","content":"hi"}]},"run":{"tool_timeout_ms":999999}}`,
			param: "run.tool_timeout_ms",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalRunRequestStrict([]byte(tc.body))
			if err == nil {
				t.Fatal("expected error")
			}
			se, ok := err.(*StrictDecodeError)
			if !ok || se.Param != tc.param {
				t.Fatalf("err=%T %#v", err, err)
			}
		})
	}
}

func TestUnmarshalRunRequestStrict_Builtins(t *testing.T) {
	_, err := UnmarshalRunRequestStrict([]byte(`{
		"request":{"model":"anthropic/x","messages":[{"role":"user","content":"hi"}]},
		"builtins":["vai_web_search", "vai_web_search"]
	}`))
	if err == nil {
		t.Fatal("expected error")
	}
	se, ok := err.(*StrictDecodeError)
	if !ok || se.Param != "builtins[1]" {
		t.Fatalf("err=%T %#v", err, err)
	}
}

func TestUnmarshalRunRequestStrict_UnknownTopLevelField(t *testing.T) {
	_, err := UnmarshalRunRequestStrict([]byte(`{
		"request":{"model":"anthropic/x","messages":[{"role":"user","content":"hi"}]},
		"bogus": 1
	}`))
	if err == nil {
		t.Fatal("expected error")
	}
	se, ok := err.(*StrictDecodeError)
	if !ok || se.Param != "bogus" {
		t.Fatalf("err=%T %#v", err, err)
	}
}
