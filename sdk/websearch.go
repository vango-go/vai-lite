package vai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
)

// VAIProvider selects the backend provider for gateway-native VAI tools.
type VAIProvider string

const (
	Tavily    VAIProvider = "tavily"
	Exa       VAIProvider = "exa"
	Firecrawl VAIProvider = "firecrawl"
)

const (
	vaiWebSearchToolName = "vai_web_search"
	vaiWebFetchToolName  = "vai_web_fetch"
)

// --- Gateway-Native VAI Web Search / Fetch ---

type gatewaySearchInput struct {
	Query      string `json:"query" desc:"The search query to execute"`
	MaxResults int    `json:"max_results,omitempty" desc:"Optional maximum number of results"`
}

type gatewayFetchInput struct {
	URL string `json:"url" desc:"The URL of the web page to fetch"`
}

// VAIWebSearch creates a gateway-native web search function tool.
//
// This tool executes via the VAI gateway (/v1/server-tools:execute), not locally.
// Requires proxy mode (`WithBaseURL(...)`) and a provider key configured via
// `WithProviderKey("tavily", ...)` or `WithProviderKey("exa", ...)`.
func VAIWebSearch(provider VAIProvider) ToolWithHandler {
	providerName := normalizeVAIProvider(provider)

	return ToolWithHandler{
		Tool: types.Tool{
			Type:        types.ToolTypeFunction,
			Name:        vaiWebSearchToolName,
			Description: "Search the web for current information. Returns normalized search results from the configured provider.",
			InputSchema: GenerateJSONSchema(reflect.TypeOf(gatewaySearchInput{})),
		},
		Handler: func(ctx context.Context, rawInput json.RawMessage) (any, error) {
			if err := validateGatewaySearchProvider(providerName); err != nil {
				return nil, err
			}
			input, err := decodeToolInputObject(rawInput)
			if err != nil {
				return nil, err
			}
			client := toolExecutionClientFromContext(ctx)
			if client == nil {
				return nil, core.NewInvalidRequestError("gateway-native VAI tools require Messages.Run or Messages.RunStream")
			}
			return client.executeGatewayServerTool(ctx, vaiWebSearchToolName, providerName, input)
		},
	}
}

// VAIWebFetch creates a gateway-native web fetch function tool.
//
// This tool executes via the VAI gateway (/v1/server-tools:execute), not locally.
// Requires proxy mode (`WithBaseURL(...)`) and a provider key configured via
// `WithProviderKey("tavily", ...)` or `WithProviderKey("firecrawl", ...)`.
func VAIWebFetch(provider VAIProvider) ToolWithHandler {
	providerName := normalizeVAIProvider(provider)

	return ToolWithHandler{
		Tool: types.Tool{
			Type:        types.ToolTypeFunction,
			Name:        vaiWebFetchToolName,
			Description: "Fetch and extract the content of a web page given its URL. Returns normalized content from the configured provider.",
			InputSchema: GenerateJSONSchema(reflect.TypeOf(gatewayFetchInput{})),
		},
		Handler: func(ctx context.Context, rawInput json.RawMessage) (any, error) {
			if err := validateGatewayFetchProvider(providerName); err != nil {
				return nil, err
			}
			input, err := decodeToolInputObject(rawInput)
			if err != nil {
				return nil, err
			}
			client := toolExecutionClientFromContext(ctx)
			if client == nil {
				return nil, core.NewInvalidRequestError("gateway-native VAI tools require Messages.Run or Messages.RunStream")
			}
			return client.executeGatewayServerTool(ctx, vaiWebFetchToolName, providerName, input)
		},
	}
}

func normalizeVAIProvider(provider VAIProvider) string {
	return strings.ToLower(strings.TrimSpace(string(provider)))
}

func validateGatewaySearchProvider(provider string) error {
	switch provider {
	case string(Tavily), string(Exa):
		return nil
	case "":
		return &core.Error{
			Type:    core.ErrInvalidRequest,
			Message: "provider is required",
			Param:   "provider",
			Code:    "tool_provider_missing",
		}
	default:
		return &core.Error{
			Type:    core.ErrInvalidRequest,
			Message: fmt.Sprintf("unsupported web search provider %q", provider),
			Param:   "provider",
			Code:    "unsupported_tool_provider",
		}
	}
}

func validateGatewayFetchProvider(provider string) error {
	switch provider {
	case string(Tavily), string(Firecrawl):
		return nil
	case "":
		return &core.Error{
			Type:    core.ErrInvalidRequest,
			Message: "provider is required",
			Param:   "provider",
			Code:    "tool_provider_missing",
		}
	default:
		return &core.Error{
			Type:    core.ErrInvalidRequest,
			Message: fmt.Sprintf("unsupported web fetch provider %q", provider),
			Param:   "provider",
			Code:    "unsupported_tool_provider",
		}
	}
}

func decodeToolInputObject(rawInput json.RawMessage) (map[string]any, error) {
	var input map[string]any
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return nil, core.NewInvalidRequestErrorWithParam("tool input must be a valid JSON object", "input")
	}
	if input == nil {
		return nil, core.NewInvalidRequestErrorWithParam("tool input must be a JSON object", "input")
	}
	return input, nil
}

// --- Local VAI-Native Web Search / Fetch ---

// WebSearchProvider performs web searches via a third-party API.
// Implement this interface to create a custom backend for LocalVAIWebSearch.
type WebSearchProvider interface {
	// Search performs a web search and returns results.
	Search(ctx context.Context, query string, opts WebSearchOpts) ([]WebSearchHit, error)
}

// WebSearchOpts configures a web search request.
type WebSearchOpts struct {
	MaxResults     int      `json:"max_results,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
	Topic          string   `json:"topic,omitempty"` // "general", "news", "finance"
}

// WebSearchHit is a single web search result.
type WebSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Content string `json:"content,omitempty"` // Optional full content
}

// LocalVAIWebSearchConfig configures the local VAI web search tool.
type LocalVAIWebSearchConfig struct {
	// ToolName overrides the default function tool name ("vai_web_search").
	ToolName string

	// ToolDescription overrides the default tool description.
	ToolDescription string

	// MaxResults sets the default max results per search.
	MaxResults int

	// AllowedDomains restricts search to these domains.
	AllowedDomains []string

	// BlockedDomains excludes these domains from search.
	BlockedDomains []string
}

// localSearchInput is the input schema for the local VAI web search tool.
type localSearchInput struct {
	Query string `json:"query" desc:"The search query to execute"`
}

// LocalVAIWebSearch creates a local VAI web search function tool backed by the given provider.
func LocalVAIWebSearch(provider WebSearchProvider, configs ...LocalVAIWebSearchConfig) ToolWithHandler {
	var cfg LocalVAIWebSearchConfig
	if len(configs) > 0 {
		cfg = configs[0]
	}

	name := vaiWebSearchToolName
	if cfg.ToolName != "" {
		name = cfg.ToolName
	}
	description := "Search the web for current information. Returns a list of relevant web pages with titles, URLs, and content snippets."
	if cfg.ToolDescription != "" {
		description = cfg.ToolDescription
	}

	maxResults := 5
	if cfg.MaxResults > 0 {
		maxResults = cfg.MaxResults
	}

	schema := GenerateJSONSchema(reflect.TypeOf(localSearchInput{}))

	handler := func(ctx context.Context, rawInput json.RawMessage) (any, error) {
		var input localSearchInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if input.Query == "" {
			return "Error: query is required", nil
		}

		opts := WebSearchOpts{
			MaxResults:     maxResults,
			AllowedDomains: cfg.AllowedDomains,
			BlockedDomains: cfg.BlockedDomains,
		}

		results, err := provider.Search(ctx, input.Query, opts)
		if err != nil {
			return fmt.Sprintf("Search error: %v", err), nil
		}

		if len(results) == 0 {
			return "No results found.", nil
		}

		// Format results for the model.
		var sb strings.Builder
		for i, r := range results {
			fmt.Fprintf(&sb, "[%d] %s\n", i+1, r.Title)
			fmt.Fprintf(&sb, "    URL: %s\n", r.URL)
			if r.Snippet != "" {
				fmt.Fprintf(&sb, "    %s\n", r.Snippet)
			}
			if r.Content != "" {
				fmt.Fprintf(&sb, "    Content: %s\n", r.Content)
			}
			sb.WriteString("\n")
		}

		return sb.String(), nil
	}

	return ToolWithHandler{
		Tool: types.Tool{
			Type:        types.ToolTypeFunction,
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: handler,
	}
}

// WebFetchProvider fetches and extracts content from URLs via a third-party API.
// Implement this interface to create a custom backend for LocalVAIWebFetch.
type WebFetchProvider interface {
	// Fetch retrieves and extracts content from the given URL.
	Fetch(ctx context.Context, url string, opts WebFetchOpts) (*WebFetchResult, error)
}

// WebFetchOpts configures a web fetch request.
type WebFetchOpts struct {
	Format string `json:"format,omitempty"` // "markdown", "text"
}

// WebFetchResult contains the extracted content from a URL.
type WebFetchResult struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content"` // Extracted content (markdown or text)
}

// LocalVAIWebFetchConfig configures the local VAI web fetch tool.
type LocalVAIWebFetchConfig struct {
	// ToolName overrides the default function tool name ("vai_web_fetch").
	ToolName string

	// ToolDescription overrides the default tool description.
	ToolDescription string

	// Format sets the default content format ("markdown" or "text").
	Format string
}

// localFetchInput is the input schema for the local VAI web fetch tool.
type localFetchInput struct {
	URL string `json:"url" desc:"The URL of the web page to fetch"`
}

// LocalVAIWebFetch creates a local VAI web fetch function tool backed by the given provider.
func LocalVAIWebFetch(provider WebFetchProvider, configs ...LocalVAIWebFetchConfig) ToolWithHandler {
	var cfg LocalVAIWebFetchConfig
	if len(configs) > 0 {
		cfg = configs[0]
	}

	name := vaiWebFetchToolName
	if cfg.ToolName != "" {
		name = cfg.ToolName
	}
	description := "Fetch and extract the content of a web page given its URL. Returns the page content in a readable format."
	if cfg.ToolDescription != "" {
		description = cfg.ToolDescription
	}

	format := "markdown"
	if cfg.Format != "" {
		format = cfg.Format
	}

	schema := GenerateJSONSchema(reflect.TypeOf(localFetchInput{}))

	handler := func(ctx context.Context, rawInput json.RawMessage) (any, error) {
		var input localFetchInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if input.URL == "" {
			return "Error: url is required", nil
		}

		opts := WebFetchOpts{Format: format}
		result, err := provider.Fetch(ctx, input.URL, opts)
		if err != nil {
			return fmt.Sprintf("Fetch error: %v", err), nil
		}

		// Format result for the model.
		var sb strings.Builder
		if result.Title != "" {
			fmt.Fprintf(&sb, "# %s\n\n", result.Title)
		}
		fmt.Fprintf(&sb, "Source: %s\n\n", result.URL)
		sb.WriteString(result.Content)

		return sb.String(), nil
	}

	return ToolWithHandler{
		Tool: types.Tool{
			Type:        types.ToolTypeFunction,
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: handler,
	}
}
