package builtins

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

const (
	BuiltinWebSearch = "vai_web_search"
	BuiltinWebFetch  = "vai_web_fetch"
)

type Executor interface {
	Name() string
	Definition() types.Tool
	Configured() bool
	Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error)
}

type Registry struct {
	byName map[string]Executor
}

func NewRegistry(executors ...Executor) *Registry {
	r := &Registry{byName: make(map[string]Executor, len(executors))}
	for _, ex := range executors {
		if ex == nil {
			continue
		}
		r.byName[ex.Name()] = ex
	}
	return r
}

func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.byName))
	for name := range r.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.byName[name]
	return ok
}

func (r *Registry) Execute(ctx context.Context, name string, input map[string]any) ([]types.ContentBlock, *types.Error) {
	if r == nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: "builtin registry is not configured", Code: "builtin_not_configured"}
	}
	ex, ok := r.byName[name]
	if !ok {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: fmt.Sprintf("function tool %q is not supported on /v1/runs", name), Code: "unsupported_function_tool"}
	}
	if !ex.Configured() {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: fmt.Sprintf("builtin %q is not configured", name), Code: "builtin_not_configured"}
	}
	return ex.Execute(ctx, input)
}

func (r *Registry) ValidateAndInject(requestTools []types.Tool, builtins []string) ([]types.Tool, error) {
	if r == nil {
		return nil, &core.Error{Type: core.ErrAPI, Message: "builtin registry is not configured", Code: "builtin_not_configured"}
	}

	selected := make(map[string]struct{}, len(builtins))
	for i, name := range builtins {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: "builtin name must be non-empty", Param: fmt.Sprintf("builtins[%d]", i), Code: "run_validation_failed"}
		}
		ex, ok := r.byName[name]
		if !ok {
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: fmt.Sprintf("unsupported builtin %q", name), Param: fmt.Sprintf("builtins[%d]", i), Code: "unsupported_builtin"}
		}
		if _, exists := selected[name]; exists {
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: "duplicate builtin name", Param: fmt.Sprintf("builtins[%d]", i), Code: "run_validation_failed"}
		}
		if !ex.Configured() {
			return nil, &core.Error{Type: core.ErrAPI, Message: fmt.Sprintf("builtin %q is not configured", name), Param: fmt.Sprintf("builtins[%d]", i), Code: "builtin_not_configured"}
		}
		selected[name] = struct{}{}
	}

	out := make([]types.Tool, 0, len(requestTools)+len(builtins))
	for i, tool := range requestTools {
		if tool.Type == types.ToolTypeFunction {
			if _, collision := selected[tool.Name]; collision {
				return nil, &core.Error{Type: core.ErrInvalidRequest, Message: "function tool name collides with selected builtin", Param: fmt.Sprintf("tools[%d].name", i), Code: "run_validation_failed"}
			}
			return nil, &core.Error{Type: core.ErrInvalidRequest, Message: fmt.Sprintf("function tool %q is not supported on /v1/runs (use /v1/messages for client-executed tools)", tool.Name), Param: fmt.Sprintf("tools[%d].name", i), Code: "unsupported_function_tool"}
		}
		out = append(out, tool)
	}

	for _, name := range builtins {
		ex := r.byName[name]
		out = append(out, ex.Definition())
	}
	return out, nil
}
