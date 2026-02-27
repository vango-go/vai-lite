package servertools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/exa"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/tavily"
)

type WebSearchExecutor struct {
	config WebSearchConfig
	tavily *tavily.Client
	exa    *exa.Client
}

func NewWebSearchExecutor(config WebSearchConfig, tavilyClient *tavily.Client, exaClient *exa.Client) *WebSearchExecutor {
	return &WebSearchExecutor{
		config: config,
		tavily: tavilyClient,
		exa:    exaClient,
	}
}

func (e *WebSearchExecutor) Name() string {
	return ToolWebSearch
}

func (e *WebSearchExecutor) Definition() types.Tool {
	additionalProps := false
	return types.Tool{
		Type:        types.ToolTypeFunction,
		Name:        ToolWebSearch,
		Description: "Search the web and return normalized search results.",
		InputSchema: &types.JSONSchema{
			Type: "object",
			Properties: map[string]types.JSONSchema{
				"query":       {Type: "string", Description: "Search query string"},
				"max_results": {Type: "integer", Description: "Optional maximum number of results"},
			},
			Required:             []string{"query"},
			AdditionalProperties: &additionalProps,
		},
	}
}

func (e *WebSearchExecutor) Execute(ctx context.Context, input map[string]any) ([]types.ContentBlock, *types.Error) {
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "query is required", Param: "query", Code: "run_validation_failed"}
	}

	requested := DefaultSearchMaxResults
	if value, ok := intFromAny(input["max_results"]); ok && value > 0 {
		requested = value
	}
	configCap := HardSearchMaxResults
	if e.config.MaxResults > 0 {
		configCap = e.config.MaxResults
	}
	maxResults := minInt(HardSearchMaxResults, minInt(configCap, requested))
	if maxResults <= 0 {
		maxResults = DefaultSearchMaxResults
	}

	type normalizedHit struct {
		Title       string
		URL         string
		Snippet     string
		PublishedAt string
		Source      string
	}
	normalized := make([]normalizedHit, 0, maxResults)
	appendHit := func(title, link, snippet, publishedAt string) {
		host, err := urlHost(link)
		if err != nil {
			return
		}
		if !hostAllowed(host, e.config.AllowedDomains, e.config.BlockedDomains) {
			return
		}
		normalized = append(normalized, normalizedHit{
			Title:       truncateString(title, 240),
			URL:         strings.TrimSpace(link),
			Snippet:     truncateString(snippet, 1000),
			PublishedAt: strings.TrimSpace(publishedAt),
			Source:      e.config.Provider,
		})
	}

	switch e.config.Provider {
	case ProviderTavily:
		if e.tavily == nil || !e.tavily.Configured() {
			return nil, &types.Error{Type: string(core.ErrAuthentication), Message: "missing provider key", Param: HeaderProviderKeyTavily, Code: "provider_key_missing"}
		}
		hits, err := e.tavily.Search(ctx, query, maxResults)
		if err != nil {
			return nil, &types.Error{Type: string(core.ErrAPI), Message: fmt.Sprintf("web search provider error: %v", err), Code: "tool_provider_error"}
		}
		for _, hit := range hits {
			appendHit(hit.Title, hit.URL, hit.Snippet, "")
			if len(normalized) >= maxResults {
				break
			}
		}
	case ProviderExa:
		if e.exa == nil || !e.exa.Configured() {
			return nil, &types.Error{Type: string(core.ErrAuthentication), Message: "missing provider key", Param: HeaderProviderKeyExa, Code: "provider_key_missing"}
		}
		hits, err := e.exa.Search(ctx, query, maxResults)
		if err != nil {
			return nil, &types.Error{Type: string(core.ErrAPI), Message: fmt.Sprintf("web search provider error: %v", err), Code: "tool_provider_error"}
		}
		for _, hit := range hits {
			snippet := hit.Snippet
			if snippet == "" {
				snippet = hit.Content
			}
			appendHit(hit.Title, hit.URL, snippet, hit.PublishedAt)
			if len(normalized) >= maxResults {
				break
			}
		}
	default:
		return nil, &types.Error{Type: string(core.ErrInvalidRequest), Message: "unsupported web search provider", Param: "provider", Code: "unsupported_tool_provider"}
	}

	payload := make(map[string]any, 2)
	payload["provider"] = e.config.Provider
	results := make([]map[string]string, 0, len(normalized))
	for _, hit := range normalized {
		results = append(results, map[string]string{
			"title":        hit.Title,
			"url":          hit.URL,
			"snippet":      hit.Snippet,
			"published_at": hit.PublishedAt,
			"source":       hit.Source,
		})
	}
	payload["results"] = results

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &types.Error{Type: string(core.ErrAPI), Message: "failed to encode web search results", Code: "tool_provider_error"}
	}
	return []types.ContentBlock{types.TextBlock{Type: "text", Text: string(encoded)}}, nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
