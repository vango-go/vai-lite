package servertools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

const (
	ToolWebSearch = "vai_web_search"
	ToolWebFetch  = "vai_web_fetch"
)

type Executor interface {
	Name() string
	Definition() types.Tool
	Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error)
}

type Registry struct {
	byName map[string]Executor
}

func NewRegistry(executors ...Executor) *Registry {
	registry := &Registry{byName: make(map[string]Executor, len(executors))}
	for _, ex := range executors {
		if ex == nil {
			continue
		}
		registry.byName[ex.Name()] = ex
	}
	return registry
}

func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.byName[strings.TrimSpace(name)]
	return ok
}

func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Definition(name string) (types.Tool, bool) {
	if r == nil {
		return types.Tool{}, false
	}
	ex, ok := r.byName[strings.TrimSpace(name)]
	if !ok {
		return types.Tool{}, false
	}
	return ex.Definition(), true
}

func (r *Registry) ValidateAndInject(requestTools []types.Tool, serverTools []string) ([]types.Tool, error) {
	if r == nil {
		return nil, &core.Error{Type: core.ErrAPI, Message: "server tools registry is not configured", Code: "server_tool_not_configured"}
	}
	selected := make(map[string]struct{}, len(serverTools))
	ordered := make([]string, 0, len(serverTools))
	for i, name := range serverTools {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: "server tool name must be non-empty", Param: fmt.Sprintf("server_tools[%d]", i), Code: "run_validation_failed"}
		}
		if _, exists := selected[name]; exists {
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: "duplicate server tool name", Param: fmt.Sprintf("server_tools[%d]", i), Code: "run_validation_failed"}
		}
		if _, ok := r.byName[name]; !ok {
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: fmt.Sprintf("unsupported server tool %q", name), Param: fmt.Sprintf("server_tools[%d]", i), Code: "unsupported_server_tool"}
		}
		selected[name] = struct{}{}
		ordered = append(ordered, name)
	}

	out := make([]types.Tool, 0, len(requestTools)+len(serverTools))
	for i, tool := range requestTools {
		if tool.Type == types.ToolTypeFunction {
			if _, collision := selected[tool.Name]; collision {
				return nil, &core.Error{Type: core.ErrInvalidRequest, Message: "function tool name collides with selected server tool", Param: fmt.Sprintf("tools[%d].name", i), Code: "run_validation_failed"}
			}
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: fmt.Sprintf("function tool %q is not supported on /v1/runs (use /v1/messages for client-executed tools)", tool.Name), Param: fmt.Sprintf("tools[%d].name", i), Code: "unsupported_function_tool"}
		}
		out = append(out, tool)
	}

	for _, name := range ordered {
		out = append(out, r.byName[name].Definition())
	}
	return out, nil
}

func (r *Registry) Execute(ctx context.Context, name string, input map[string]any) ([]types.ContentBlock, *types.Error) {
	if r == nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: "server tools registry is not configured", Code: "server_tool_not_configured"}
	}
	ex, ok := r.byName[strings.TrimSpace(name)]
	if !ok {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: fmt.Sprintf("function tool %q is not supported on /v1/runs", name), Code: "unsupported_function_tool"}
	}
	return ex.Execute(ctx, input)
}
