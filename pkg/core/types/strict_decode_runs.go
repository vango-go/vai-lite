package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	defaultRunMaxTurns      = 8
	defaultRunMaxToolCalls  = 20
	defaultRunMaxTokens     = 0
	defaultRunTimeoutMS     = 60000
	defaultRunParallelTools = true
	defaultRunToolTimeoutMS = 30000

	minRunMaxTurns      = 1
	maxRunMaxTurns      = 64
	minRunMaxToolCalls  = 1
	maxRunMaxToolCalls  = 256
	minRunTimeoutMS     = 1000
	maxRunTimeoutMS     = 300000
	minRunToolTimeoutMS = 1000
	maxRunToolTimeoutMS = 60000
)

// UnmarshalRunRequestStrict strictly decodes a RunRequest.
func UnmarshalRunRequestStrict(data []byte) (*RunRequest, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}

	for k := range top {
		switch k {
		case "request", "run", "builtins":
		default:
			return nil, strictErr(k, fmt.Sprintf("unknown field %q", k))
		}
	}

	reqRaw, ok := top["request"]
	if !ok || isNullOrEmptyJSON(reqRaw) {
		return nil, strictErr("request", "request is required")
	}
	msgReq, err := UnmarshalMessageRequestStrict(reqRaw)
	if err != nil {
		if se, ok := err.(*StrictDecodeError); ok {
			if strings.TrimSpace(se.Param) == "" {
				se.Param = "request"
			} else {
				se.Param = "request." + se.Param
			}
			return nil, se
		}
		return nil, err
	}
	if msgReq.Stream {
		return nil, strictErr("request.stream", "request.stream must be false or omitted")
	}

	runCfg := RunConfig{
		MaxTurns:      defaultRunMaxTurns,
		MaxToolCalls:  defaultRunMaxToolCalls,
		MaxTokens:     defaultRunMaxTokens,
		TimeoutMS:     defaultRunTimeoutMS,
		ParallelTools: defaultRunParallelTools,
		ToolTimeoutMS: defaultRunToolTimeoutMS,
	}

	if runRaw, ok := top["run"]; ok && !isNullOrEmptyJSON(runRaw) {
		dec := json.NewDecoder(bytes.NewReader(runRaw))
		dec.DisallowUnknownFields()
		var raw struct {
			MaxTurns      *int  `json:"max_turns,omitempty"`
			MaxToolCalls  *int  `json:"max_tool_calls,omitempty"`
			MaxTokens     *int  `json:"max_tokens,omitempty"`
			TimeoutMS     *int  `json:"timeout_ms,omitempty"`
			ParallelTools *bool `json:"parallel_tools,omitempty"`
			ToolTimeoutMS *int  `json:"tool_timeout_ms,omitempty"`
		}
		if err := dec.Decode(&raw); err != nil {
			return nil, strictErr("run", fmt.Sprintf("invalid run config: %v", err))
		}
		if raw.MaxTurns != nil {
			runCfg.MaxTurns = *raw.MaxTurns
		}
		if raw.MaxToolCalls != nil {
			runCfg.MaxToolCalls = *raw.MaxToolCalls
		}
		if raw.MaxTokens != nil {
			runCfg.MaxTokens = *raw.MaxTokens
		}
		if raw.TimeoutMS != nil {
			runCfg.TimeoutMS = *raw.TimeoutMS
		}
		if raw.ParallelTools != nil {
			runCfg.ParallelTools = *raw.ParallelTools
		}
		if raw.ToolTimeoutMS != nil {
			runCfg.ToolTimeoutMS = *raw.ToolTimeoutMS
		}
	}

	if runCfg.MaxTurns < minRunMaxTurns || runCfg.MaxTurns > maxRunMaxTurns {
		return nil, strictErr("run.max_turns", fmt.Sprintf("run.max_turns must be between %d and %d", minRunMaxTurns, maxRunMaxTurns))
	}
	if runCfg.MaxToolCalls < minRunMaxToolCalls || runCfg.MaxToolCalls > maxRunMaxToolCalls {
		return nil, strictErr("run.max_tool_calls", fmt.Sprintf("run.max_tool_calls must be between %d and %d", minRunMaxToolCalls, maxRunMaxToolCalls))
	}
	if runCfg.TimeoutMS < minRunTimeoutMS || runCfg.TimeoutMS > maxRunTimeoutMS {
		return nil, strictErr("run.timeout_ms", fmt.Sprintf("run.timeout_ms must be between %d and %d", minRunTimeoutMS, maxRunTimeoutMS))
	}
	if runCfg.ToolTimeoutMS < minRunToolTimeoutMS || runCfg.ToolTimeoutMS > maxRunToolTimeoutMS {
		return nil, strictErr("run.tool_timeout_ms", fmt.Sprintf("run.tool_timeout_ms must be between %d and %d", minRunToolTimeoutMS, maxRunToolTimeoutMS))
	}

	out := &RunRequest{
		Request: *msgReq,
		Run:     runCfg,
	}

	if builtinsRaw, ok := top["builtins"]; ok && !isNullOrEmptyJSON(builtinsRaw) {
		var builtins []string
		if err := json.Unmarshal(builtinsRaw, &builtins); err != nil {
			return nil, strictErr("builtins", "builtins must be an array of strings")
		}
		seen := make(map[string]struct{}, len(builtins))
		for i, name := range builtins {
			name = strings.TrimSpace(name)
			if name == "" {
				return nil, strictErr(fmt.Sprintf("builtins[%d]", i), "builtin name must be non-empty")
			}
			if _, exists := seen[name]; exists {
				return nil, strictErr(fmt.Sprintf("builtins[%d]", i), "duplicate builtin name")
			}
			seen[name] = struct{}{}
			out.Builtins = append(out.Builtins, name)
		}
	}

	return out, nil
}
