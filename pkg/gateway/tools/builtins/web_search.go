package builtins

import (
	"context"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/tavily"
)

type WebSearchBuiltin struct {
	client *tavily.Client
}

func NewWebSearchBuiltin(client *tavily.Client) *WebSearchBuiltin {
	return &WebSearchBuiltin{client: client}
}

func (b *WebSearchBuiltin) Name() string {
	return BuiltinWebSearch
}

func (b *WebSearchBuiltin) Configured() bool {
	return b != nil && b.client != nil && b.client.Configured()
}

func (b *WebSearchBuiltin) Definition() types.Tool {
	return types.Tool{
		Type:        types.ToolTypeFunction,
		Name:        BuiltinWebSearch,
		Description: "Search the web for current information and return concise result snippets.",
		InputSchema: &types.JSONSchema{
			Type: "object",
			Properties: map[string]types.JSONSchema{
				"query": {Type: "string", Description: "The search query"},
				"max_results": {Type: "integer", Description: "Optional max results"},
			},
			Required: []string{"query"},
		},
	}
}

func (b *WebSearchBuiltin) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	if !b.Configured() {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: "builtin vai_web_search is not configured", Code: "builtin_not_configured"}
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "query is required", Param: "query", Code: "run_validation_failed"}
	}
	maxResults := 5
	if v, ok := input["max_results"].(float64); ok && int(v) > 0 {
		maxResults = int(v)
	}

	hits, err := b.client.Search(ctx, query, maxResults)
	if err != nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: fmt.Sprintf("web search failed: %v", err)}
	}

	if len(hits) == 0 {
		return []types.ContentBlock{types.TextBlock{Type: "text", Text: "No results found."}}, nil
	}

	var sb strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&sb, "[%d] %s\n", i+1, strings.TrimSpace(h.Title))
		fmt.Fprintf(&sb, "URL: %s\n", strings.TrimSpace(h.URL))
		if strings.TrimSpace(h.Snippet) != "" {
			fmt.Fprintf(&sb, "%s\n", strings.TrimSpace(h.Snippet))
		}
		if strings.TrimSpace(h.Content) != "" {
			fmt.Fprintf(&sb, "Content: %s\n", strings.TrimSpace(h.Content))
		}
		sb.WriteString("\n")
	}

	return []types.ContentBlock{types.TextBlock{Type: "text", Text: strings.TrimSpace(sb.String())}}, nil
}
