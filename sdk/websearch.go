package vai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

// --- VAI-Native Web Search ---

// WebSearchProvider performs web searches via a third-party API.
// Implement this interface to create a custom search backend for VAIWebSearch.
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

// VAIWebSearchConfig configures the VAI-native web search tool.
type VAIWebSearchConfig struct {
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

// vaiSearchInput is the input schema for the VAI web search tool.
type vaiSearchInput struct {
	Query string `json:"query" desc:"The search query to execute"`
}

// VAIWebSearch creates a VAI-native web search function tool backed by the given provider.
//
// Unlike vai.WebSearch() which relies on the LLM provider's native search capability,
// VAIWebSearch creates a function tool that VAI executes locally using the specified
// third-party search provider (e.g., Tavily, Exa, Brave).
//
// This works with ANY LLM provider, including those without native web search support.
//
// Example:
//
//	search := vai.VAIWebSearch(tavily.NewSearch(apiKey))
//	result, err := client.Messages.Run(ctx, req, vai.WithTools(search))
func VAIWebSearch(provider WebSearchProvider, configs ...VAIWebSearchConfig) ToolWithHandler {
	var cfg VAIWebSearchConfig
	if len(configs) > 0 {
		cfg = configs[0]
	}

	name := "vai_web_search"
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

	schema := GenerateJSONSchema(reflect.TypeOf(vaiSearchInput{}))

	handler := func(ctx context.Context, rawInput json.RawMessage) (any, error) {
		var input vaiSearchInput
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

		// Format results for the model
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

// --- VAI-Native Web Fetch ---

// WebFetchProvider fetches and extracts content from URLs via a third-party API.
// Implement this interface to create a custom fetch backend for VAIWebFetch.
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

// VAIWebFetchConfig configures the VAI-native web fetch tool.
type VAIWebFetchConfig struct {
	// ToolName overrides the default function tool name ("vai_web_fetch").
	ToolName string

	// ToolDescription overrides the default tool description.
	ToolDescription string

	// Format sets the default content format ("markdown" or "text").
	Format string
}

// vaiFetchInput is the input schema for the VAI web fetch tool.
type vaiFetchInput struct {
	URL string `json:"url" desc:"The URL of the web page to fetch"`
}

// VAIWebFetch creates a VAI-native web fetch function tool backed by the given provider.
//
// Unlike vai.WebFetch() which relies on the LLM provider's native fetch capability
// (currently only Anthropic), VAIWebFetch creates a function tool that VAI executes
// locally using the specified third-party provider (e.g., Firecrawl, Tavily Extract).
//
// This works with ANY LLM provider.
//
// Example:
//
//	fetch := vai.VAIWebFetch(firecrawl.NewFetch(apiKey))
//	result, err := client.Messages.Run(ctx, req, vai.WithTools(fetch))
func VAIWebFetch(provider WebFetchProvider, configs ...VAIWebFetchConfig) ToolWithHandler {
	var cfg VAIWebFetchConfig
	if len(configs) > 0 {
		cfg = configs[0]
	}

	name := "vai_web_fetch"
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

	schema := GenerateJSONSchema(reflect.TypeOf(vaiFetchInput{}))

	handler := func(ctx context.Context, rawInput json.RawMessage) (any, error) {
		var input vaiFetchInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if input.URL == "" {
			return "Error: url is required", nil
		}

		opts := WebFetchOpts{
			Format: format,
		}

		result, err := provider.Fetch(ctx, input.URL, opts)
		if err != nil {
			return fmt.Sprintf("Fetch error: %v", err), nil
		}

		// Format result for the model
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
