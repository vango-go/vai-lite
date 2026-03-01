package servertools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/tavily"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/firecrawl"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
)

type WebFetchExecutor struct {
	config    WebFetchConfig
	firecrawl *firecrawl.Client
	tavily    *tavily.Client
}

func NewWebFetchExecutor(config WebFetchConfig, firecrawlClient *firecrawl.Client, tavilyClient *tavily.Client) *WebFetchExecutor {
	return &WebFetchExecutor{
		config:    config,
		firecrawl: firecrawlClient,
		tavily:    tavilyClient,
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

	var (
		providerName string
		resultURL    string
		resultTitle  string
		resultBody   string
		fetchErr     error
	)
	switch e.config.Provider {
	case ProviderFirecrawl:
		if e.firecrawl == nil || !e.firecrawl.Configured() {
			return nil, &types.Error{Type: string(core.ErrAuthentication), Message: "missing provider key", Param: HeaderProviderKeyFirecrawl, Code: "provider_key_missing"}
		}
		fetchResult, err := e.firecrawl.Fetch(ctx, targetURL)
		if err != nil {
			fetchErr = err
			break
		}
		providerName = ProviderFirecrawl
		resultURL = strings.TrimSpace(fetchResult.URL)
		resultTitle = strings.TrimSpace(fetchResult.Title)
		resultBody = fetchResult.Content
	case ProviderTavily:
		if e.tavily == nil || !e.tavily.Configured() {
			return nil, &types.Error{Type: string(core.ErrAuthentication), Message: "missing provider key", Param: HeaderProviderKeyTavily, Code: "provider_key_missing"}
		}
		fetchResult, err := e.tavily.Extract(ctx, targetURL, e.config.Format)
		if err != nil {
			fetchErr = err
			break
		}
		providerName = ProviderTavily
		resultURL = strings.TrimSpace(fetchResult.URL)
		resultTitle = strings.TrimSpace(fetchResult.Title)
		resultBody = fetchResult.Content
	default:
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "unsupported web fetch provider", Param: "provider", Code: "unsupported_tool_provider"}
	}
	if fetchErr != nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: fmt.Sprintf("web fetch provider error: %v", fetchErr), Code: "tool_provider_error"}
	}

	content := truncateString(resultBody, 120000)
	format := e.config.Format
	if format == "" {
		format = "markdown"
	}
	payload := map[string]any{
		"provider": providerName,
		"url":      resultURL,
		"title":    resultTitle,
		"format":   format,
		"content":  content,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: "failed to encode web fetch result", Code: "tool_provider_error"}
	}
	return []types.ContentBlock{types.TextBlock{Type: "text", Text: string(encoded)}}, nil
}
