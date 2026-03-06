package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ServerToolExecuteRequest is the request body for /v1/server-tools:execute.
type ServerToolExecuteRequest struct {
	Tool             string                      `json:"tool"`
	Input            map[string]any              `json:"input"`
	ServerToolConfig map[string]any              `json:"server_tool_config,omitempty"`
	ExecutionContext *ServerToolExecutionContext `json:"execution_context,omitempty"`
}

// ServerToolExecuteResponse is the response body for /v1/server-tools:execute.
type ServerToolExecuteResponse struct {
	Content []ContentBlock `json:"content"`
}

// ServerToolExecutionContext carries request-scoped execution state that is not
// part of the model-visible tool input.
type ServerToolExecutionContext struct {
	Images []ServerToolExecutionImage `json:"images,omitempty"`
}

// ServerToolExecutionImage binds a synthetic image id to an actual image block.
type ServerToolExecutionImage struct {
	ID    string     `json:"id"`
	Image ImageBlock `json:"image"`
}

// UnmarshalServerToolExecuteRequestStrict strictly decodes a ServerToolExecuteRequest.
func UnmarshalServerToolExecuteRequestStrict(data []byte) (*ServerToolExecuteRequest, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}

	for k := range top {
		switch k {
		case "tool", "input", "server_tool_config", "execution_context":
		default:
			return nil, strictErr(k, fmt.Sprintf("unknown field %q", k))
		}
	}

	toolRaw, ok := top["tool"]
	if !ok || isNullOrEmptyJSON(toolRaw) {
		return nil, strictErr("tool", "tool is required")
	}
	var tool string
	if err := json.Unmarshal(toolRaw, &tool); err != nil {
		return nil, strictErr("tool", "tool must be a string")
	}
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return nil, strictErr("tool", "tool must be non-empty")
	}

	inputRaw, ok := top["input"]
	if !ok || isNullOrEmptyJSON(inputRaw) {
		return nil, strictErr("input", "input is required")
	}
	var input map[string]any
	if err := json.Unmarshal(inputRaw, &input); err != nil {
		return nil, strictErr("input", "input must be an object")
	}
	if input == nil {
		return nil, strictErr("input", "input must be an object")
	}

	out := &ServerToolExecuteRequest{
		Tool:  tool,
		Input: input,
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

	if execRaw, ok := top["execution_context"]; ok && !isNullOrEmptyJSON(execRaw) {
		execCtx, err := unmarshalServerToolExecutionContextStrict(execRaw)
		if err != nil {
			return nil, err
		}
		out.ExecutionContext = execCtx
	}

	return out, nil
}

func unmarshalServerToolExecutionContextStrict(data []byte) (*ServerToolExecutionContext, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, strictErr("execution_context", "execution_context must be an object")
	}
	for k := range top {
		switch k {
		case "images":
		default:
			return nil, strictErr("execution_context."+k, fmt.Sprintf("unknown field %q", k))
		}
	}

	ctx := &ServerToolExecutionContext{}
	imagesRaw, ok := top["images"]
	if !ok || isNullOrEmptyJSON(imagesRaw) {
		return ctx, nil
	}

	var rawImages []json.RawMessage
	if err := json.Unmarshal(imagesRaw, &rawImages); err != nil {
		return nil, strictErr("execution_context.images", "execution_context.images must be an array")
	}

	ctx.Images = make([]ServerToolExecutionImage, 0, len(rawImages))
	seenIDs := make(map[string]struct{}, len(rawImages))
	for i, rawImage := range rawImages {
		entry, err := unmarshalServerToolExecutionImageStrict(i, rawImage)
		if err != nil {
			return nil, err
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return nil, strictErr("execution_context.images", "execution_context.images contains duplicate ids")
		}
		seenIDs[entry.ID] = struct{}{}
		ctx.Images = append(ctx.Images, *entry)
	}

	return ctx, nil
}

func unmarshalServerToolExecutionImageStrict(index int, data []byte) (*ServerToolExecutionImage, error) {
	param := fmt.Sprintf("execution_context.images[%d]", index)

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, strictErr(param, param+" must be an object")
	}
	for k := range top {
		switch k {
		case "id", "image":
		default:
			return nil, strictErr(param+"."+k, fmt.Sprintf("unknown field %q", k))
		}
	}

	idRaw, ok := top["id"]
	if !ok || isNullOrEmptyJSON(idRaw) {
		return nil, strictErr(param+".id", "execution_context image id is required")
	}
	var id string
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return nil, strictErr(param+".id", "execution_context image id must be a string")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, strictErr(param+".id", "execution_context image id must be non-empty")
	}

	imageRaw, ok := top["image"]
	if !ok || isNullOrEmptyJSON(imageRaw) {
		return nil, strictErr(param+".image", "execution_context image is required")
	}
	block, err := UnmarshalContentBlock(imageRaw)
	if err != nil {
		return nil, strictErr(param+".image", "execution_context image must be a valid content block")
	}
	imageBlock, ok := block.(ImageBlock)
	if !ok {
		return nil, strictErr(param+".image", "execution_context image must be an image block")
	}

	return &ServerToolExecutionImage{ID: id, Image: imageBlock}, nil
}

// UnmarshalServerToolExecuteResponse decodes a server-tool execute response.
func UnmarshalServerToolExecuteResponse(data []byte) (*ServerToolExecuteResponse, error) {
	var raw struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	content, err := unmarshalContentBlocks(raw.Content)
	if err != nil {
		return nil, err
	}
	return &ServerToolExecuteResponse{Content: content}, nil
}
