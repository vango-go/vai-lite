package servertools

import (
	"context"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

type fakeExecutor struct {
	name string
}

func (f fakeExecutor) Name() string { return f.name }
func (f fakeExecutor) Definition() types.Tool {
	return types.Tool{Type: types.ToolTypeFunction, Name: f.name, Description: "d", InputSchema: &types.JSONSchema{Type: "object"}}
}
func (f fakeExecutor) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	return []types.ContentBlock{types.TextBlock{Type: "text", Text: "ok"}}, nil
}

func TestRegistryValidateAndInject_RejectsFunctionTools(t *testing.T) {
	t.Parallel()

	r := NewRegistry(fakeExecutor{name: ToolWebSearch})
	tools := []types.Tool{{Type: types.ToolTypeFunction, Name: "my_fn", Description: "d", InputSchema: &types.JSONSchema{Type: "object"}}}
	if _, err := r.ValidateAndInject(tools, []string{ToolWebSearch}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistryValidateAndInject_UnsupportedTool(t *testing.T) {
	t.Parallel()

	r := NewRegistry(fakeExecutor{name: ToolWebSearch})
	if _, err := r.ValidateAndInject(nil, []string{"nope"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestDecodeWebSearchConfig_RejectsUnknownField(t *testing.T) {
	t.Parallel()

	if _, err := DecodeWebSearchConfig(map[string]any{"provider": "tavily", "unknown": true}); err == nil {
		t.Fatal("expected error")
	}
}

func TestHostAllowed_SubdomainMatching(t *testing.T) {
	t.Parallel()

	if !hostAllowed("docs.example.com", []string{"example.com"}, nil) {
		t.Fatal("expected subdomain allow match")
	}
	if hostAllowed("docs.example.com", nil, []string{"example.com"}) {
		t.Fatal("expected subdomain block match")
	}
}
