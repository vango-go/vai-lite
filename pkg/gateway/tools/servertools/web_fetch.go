package servertools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/firecrawl"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
)

type WebFetchExecutor struct {
	config    WebFetchConfig
	firecrawl *firecrawl.Client
}

func NewWebFetchExecutor(config WebFetchConfig, firecrawlClient *firecrawl.Client) *WebFetchExecutor {
	return &WebFetchExecutor{
		config:    config,
		firecrawl: firecrawlClient,
	}
}

func (e *WebFetchExecutor) Name() string {
	return ToolWebFetch
}

func (e *WebFetchExecutor) Definition() types.Tool {
	additionalProps := false
	return types.Tool{
		Type:        types.ToolTypeFunction,
		Name:        ToolWebFetch,
		Description: "Fetch page content for a URL and return normalized content.",
		InputSchema: &types.JSONSchema{
			Type: "object",
			Properties: map[string]types.JSONSchema{
				"url": {Type: "string", Description: "URL to fetch"},
			},
			Required:             []string{"url"},
			AdditionalProperties: &additionalProps,
		},
	}
}

func (e *WebFetchExecutor) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	targetURL, _ := input["url"].(string)
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "url is required", Param: "url", Code: "run_validation_failed"}
	}

	parsed, err := safety.ValidateTargetURL(ctx, targetURL)
	if err != nil {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: fmt.Sprintf("invalid url: %v", err), Param: "url", Code: "run_validation_failed"}
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if !hostAllowed(host, e.config.AllowedDomains, e.config.BlockedDomains) {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "url host is not allowed by server tool policy", Param: "url", Code: "run_validation_failed"}
	}

	if e.config.Provider != ProviderFirecrawl {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "unsupported web fetch provider", Param: "provider", Code: "unsupported_tool_provider"}
	}
	if e.firecrawl == nil || !e.firecrawl.Configured() {
		return nil, &types.Error{Type: string(core.ErrAuthentication), Message: "missing provider key", Param: HeaderProviderKeyFirecrawl, Code: "provider_key_missing"}
	}

	fetchResult, fetchErr := e.firecrawl.Fetch(ctx, targetURL)
	if fetchErr != nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: fmt.Sprintf("web fetch provider error: %v", fetchErr), Code: "tool_provider_error"}
	}

	content := truncateString(fetchResult.Content, 120000)
	format := e.config.Format
	if format == "" {
		format = "markdown"
	}
	payload := map[string]any{
		"provider": e.config.Provider,
		"url":      strings.TrimSpace(fetchResult.URL),
		"title":    strings.TrimSpace(fetchResult.Title),
		"format":   format,
		"content":  content,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: "failed to encode web fetch result", Code: "tool_provider_error"}
	}
	return []types.ContentBlock{types.TextBlock{Type: "text", Text: string(encoded)}}, nil
}
