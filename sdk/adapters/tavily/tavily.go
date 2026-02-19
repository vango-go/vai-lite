// Package tavily provides Tavily API adapters for VAI web search and fetch tools.
//
// Tavily (https://tavily.com) is an AI-native search engine designed for LLMs.
// It provides both a Search API and an Extract API.
//
// Usage:
//
//	import "github.com/vango-go/vai-lite/sdk/adapters/tavily"
//
//	// Web search
//	search := vai.VAIWebSearch(tavily.NewSearch(os.Getenv("TAVILY_API_KEY")))
//
//	// Web fetch (content extraction)
//	fetch := vai.VAIWebFetch(tavily.NewExtract(os.Getenv("TAVILY_API_KEY")))
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	vai "github.com/vango-go/vai-lite/sdk"
)

const (
	defaultBaseURL = "https://api.tavily.com"
)

// Option configures a Tavily client.
type Option func(*options)

type options struct {
	baseURL    string
	httpClient *http.Client
}

// WithBaseURL overrides the Tavily API base URL.
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) { o.httpClient = client }
}

// --- Search Adapter ---

// Search implements vai.WebSearchProvider using the Tavily Search API.
type Search struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewSearch creates a new Tavily Search provider.
func NewSearch(apiKey string, opts ...Option) *Search {
	o := &options{
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(o)
	}
	return &Search{
		apiKey:  apiKey,
		baseURL: o.baseURL,
		client:  o.httpClient,
	}
}

// tavilySearchRequest is the Tavily /search request body.
type tavilySearchRequest struct {
	Query          string   `json:"query"`
	SearchDepth    string   `json:"search_depth,omitempty"`
	Topic          string   `json:"topic,omitempty"`
	MaxResults     int      `json:"max_results,omitempty"`
	IncludeAnswer  any      `json:"include_answer,omitempty"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

// tavilySearchResponse is the Tavily /search response body.
type tavilySearchResponse struct {
	Query   string              `json:"query"`
	Answer  string              `json:"answer,omitempty"`
	Results []tavilySearchEntry `json:"results"`
}

// tavilySearchEntry is a single search result from Tavily.
type tavilySearchEntry struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	RawContent string  `json:"raw_content,omitempty"`
}

// Search implements vai.WebSearchProvider.
func (s *Search) Search(ctx context.Context, query string, opts vai.WebSearchOpts) ([]vai.WebSearchHit, error) {
	reqBody := tavilySearchRequest{
		Query:       query,
		SearchDepth: "basic",
		MaxResults:  opts.MaxResults,
	}
	if reqBody.MaxResults <= 0 {
		reqBody.MaxResults = 5
	}
	if opts.Topic != "" {
		reqBody.Topic = opts.Topic
	}
	if len(opts.AllowedDomains) > 0 {
		reqBody.IncludeDomains = opts.AllowedDomains
	}
	if len(opts.BlockedDomains) > 0 {
		reqBody.ExcludeDomains = opts.BlockedDomains
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("tavily: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tavily: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily: API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tavilyResp tavilySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, fmt.Errorf("tavily: decode response: %w", err)
	}

	hits := make([]vai.WebSearchHit, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		hits = append(hits, vai.WebSearchHit{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
			Content: r.RawContent,
		})
	}

	return hits, nil
}

// --- Extract Adapter ---

// Extract implements vai.WebFetchProvider using the Tavily Extract API.
type Extract struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewExtract creates a new Tavily Extract provider.
func NewExtract(apiKey string, opts ...Option) *Extract {
	o := &options{
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(o)
	}
	return &Extract{
		apiKey:  apiKey,
		baseURL: o.baseURL,
		client:  o.httpClient,
	}
}

// tavilyExtractRequest is the Tavily /extract request body.
type tavilyExtractRequest struct {
	URLs         []string `json:"urls"`
	ExtractDepth string   `json:"extract_depth,omitempty"`
	Format       string   `json:"format,omitempty"`
}

// tavilyExtractResponse is the Tavily /extract response body.
type tavilyExtractResponse struct {
	Results       []tavilyExtractEntry `json:"results"`
	FailedResults []struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	} `json:"failed_results"`
}

// tavilyExtractEntry is a single extraction result from Tavily.
type tavilyExtractEntry struct {
	URL        string `json:"url"`
	RawContent string `json:"raw_content"`
}

// Fetch implements vai.WebFetchProvider.
func (e *Extract) Fetch(ctx context.Context, url string, opts vai.WebFetchOpts) (*vai.WebFetchResult, error) {
	format := "markdown"
	if opts.Format != "" {
		format = opts.Format
	}

	reqBody := tavilyExtractRequest{
		URLs:         []string{url},
		ExtractDepth: "basic",
		Format:       format,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("tavily: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/extract", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tavily: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily: API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tavilyResp tavilyExtractResponse
	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, fmt.Errorf("tavily: decode response: %w", err)
	}

	// Check for failures
	if len(tavilyResp.FailedResults) > 0 && len(tavilyResp.Results) == 0 {
		return nil, fmt.Errorf("tavily: extraction failed for %s: %s", url, tavilyResp.FailedResults[0].Error)
	}

	if len(tavilyResp.Results) == 0 {
		return nil, fmt.Errorf("tavily: no content extracted from %s", url)
	}

	result := tavilyResp.Results[0]
	return &vai.WebFetchResult{
		URL:     result.URL,
		Content: result.RawContent,
	}, nil
}
