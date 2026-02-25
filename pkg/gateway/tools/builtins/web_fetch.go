package builtins

import (
	"context"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/firecrawl"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
)

type WebFetchBuiltin struct {
	client *firecrawl.Client
}

func NewWebFetchBuiltin(client *firecrawl.Client) *WebFetchBuiltin {
	return &WebFetchBuiltin{client: client}
}

func (b *WebFetchBuiltin) Name() string {
	return BuiltinWebFetch
}

func (b *WebFetchBuiltin) Configured() bool {
	return b != nil && b.client != nil && b.client.Configured()
}

func (b *WebFetchBuiltin) Definition() types.Tool {
	return types.Tool{
		Type:        types.ToolTypeFunction,
		Name:        BuiltinWebFetch,
		Description: "Fetch and extract readable content from a web page URL.",
		InputSchema: &types.JSONSchema{
			Type: "object",
			Properties: map[string]types.JSONSchema{
				"url": {Type: "string", Description: "The URL to fetch"},
			},
			Required: []string{"url"},
		},
	}
}

func (b *WebFetchBuiltin) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	if !b.Configured() {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: "builtin vai_web_fetch is not configured", Code: "builtin_not_configured"}
	}
	targetURL, _ := input["url"].(string)
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "url is required", Param: "url", Code: "run_validation_failed"}
	}
	if _, err := safety.ValidateTargetURL(ctx, targetURL); err != nil {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: fmt.Sprintf("invalid url: %v", err), Param: "url", Code: "run_validation_failed"}
	}

	res, err := b.client.Fetch(ctx, targetURL)
	if err != nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: fmt.Sprintf("web fetch failed: %v", err)}
	}

	var sb strings.Builder
	if strings.TrimSpace(res.Title) != "" {
		fmt.Fprintf(&sb, "# %s\n\n", strings.TrimSpace(res.Title))
	}
	fmt.Fprintf(&sb, "Source: %s\n\n", strings.TrimSpace(res.URL))
	sb.WriteString(strings.TrimSpace(res.Content))

	return []types.ContentBlock{types.TextBlock{Type: "text", Text: strings.TrimSpace(sb.String())}}, nil
}
