package builtins

import (
	"context"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

type fakeExecutor struct {
	name       string
	configured bool
}

func (f fakeExecutor) Name() string { return f.name }
func (f fakeExecutor) Definition() types.Tool {
	return types.Tool{Type: types.ToolTypeFunction, Name: f.name, Description: "d", InputSchema: &types.JSONSchema{Type: "object"}}
}
func (f fakeExecutor) Configured() bool { return f.configured }
func (f fakeExecutor) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	return []types.ContentBlock{types.TextBlock{Type: "text", Text: "ok"}}, nil
}

func TestRegistryValidateAndInject_UnsupportedBuiltin(t *testing.T) {
	r := NewRegistry(fakeExecutor{name: BuiltinWebSearch, configured: true})
	_, err := r.ValidateAndInject(nil, []string{"nope"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistryValidateAndInject_RejectsFunctionTools(t *testing.T) {
	r := NewRegistry(fakeExecutor{name: BuiltinWebSearch, configured: true})
	tools := []types.Tool{{Type: types.ToolTypeFunction, Name: "my_fn", Description: "d", InputSchema: &types.JSONSchema{Type: "object"}}}
	_, err := r.ValidateAndInject(tools, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistryValidateAndInject_Collision(t *testing.T) {
	r := NewRegistry(fakeExecutor{name: BuiltinWebSearch, configured: true})
	tools := []types.Tool{{Type: types.ToolTypeFunction, Name: BuiltinWebSearch, Description: "d", InputSchema: &types.JSONSchema{Type: "object"}}}
	_, err := r.ValidateAndInject(tools, []string{BuiltinWebSearch})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistryValidateAndInject_ConfigRequired(t *testing.T) {
	r := NewRegistry(fakeExecutor{name: BuiltinWebSearch, configured: false})
	_, err := r.ValidateAndInject(nil, []string{BuiltinWebSearch})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistryValidateAndInject_Success(t *testing.T) {
	r := NewRegistry(fakeExecutor{name: BuiltinWebSearch, configured: true})
	tools := []types.Tool{{Type: types.ToolTypeWebSearch}}
	out, err := r.ValidateAndInject(tools, []string{BuiltinWebSearch})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out)=%d", len(out))
	}
}
