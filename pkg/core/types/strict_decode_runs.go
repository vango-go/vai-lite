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
		case "request", "run", "builtins", "server_tools", "server_tool_config":
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

	if serverToolsRaw, ok := top["server_tools"]; ok && !isNullOrEmptyJSON(serverToolsRaw) {
		serverTools, err := parseRunToolNames(serverToolsRaw, "server_tools")
		if err != nil {
			return nil, err
		}
		out.ServerTools = serverTools
	}

	if builtinsRaw, ok := top["builtins"]; ok && !isNullOrEmptyJSON(builtinsRaw) {
		builtins, err := parseRunToolNames(builtinsRaw, "builtins")
		if err != nil {
			return nil, err
		}
		out.Builtins = builtins
	}

	if len(out.ServerTools) > 0 && len(out.Builtins) > 0 && !equivalentToolSets(out.ServerTools, out.Builtins) {
		return nil, strictErr("server_tools", "server_tools and builtins must match when both are provided")
	}
	if len(out.ServerTools) == 0 && len(out.Builtins) > 0 {
		out.ServerTools = append([]string(nil), out.Builtins...)
	}

	if cfgRaw, ok := top["server_tool_config"]; ok && !isNullOrEmptyJSON(cfgRaw) {
		var rawCfg map[string]json.RawMessage
		if err := json.Unmarshal(cfgRaw, &rawCfg); err != nil {
			return nil, strictErr("server_tool_config", "server_tool_config must be an object")
		}
		out.ServerToolConfig = make(map[string]any, len(rawCfg))
		for name, value := range rawCfg {
			toolName := strings.TrimSpace(name)
			if toolName == "" {
				return nil, strictErr("server_tool_config", "server_tool_config keys must be non-empty")
			}
			if isNullOrEmptyJSON(value) {
				return nil, strictErr("server_tool_config."+toolName, "server_tool_config entries must be objects")
			}
			var obj map[string]any
			if err := json.Unmarshal(value, &obj); err != nil {
				return nil, strictErr("server_tool_config."+toolName, "server_tool_config entries must be objects")
			}
			if obj == nil {
				return nil, strictErr("server_tool_config."+toolName, "server_tool_config entries must be objects")
			}
			out.ServerToolConfig[toolName] = obj
		}
	}

	return out, nil
}

func parseRunToolNames(raw json.RawMessage, field string) ([]string, error) {
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, strictErr(field, field+" must be an array of strings")
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, strictErr(fmt.Sprintf("%s[%d]", field, i), "tool name must be non-empty")
		}
		if _, exists := seen[name]; exists {
			return nil, strictErr(fmt.Sprintf("%s[%d]", field, i), "duplicate tool name")
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func equivalentToolSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	set := make(map[string]struct{}, len(left))
	for _, name := range left {
		set[name] = struct{}{}
	}
	for _, name := range right {
		if _, ok := set[name]; !ok {
			return false
		}
	}
	return true
}
