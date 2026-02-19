// Package exa provides Exa AI API adapters for VAI web search and fetch tools.
//
// Exa (https://exa.ai) is an AI-native search engine with its own neural
// search index. It provides semantic search and content extraction.
//
// Usage:
//
//	import "github.com/vango-go/vai-lite/sdk/adapters/exa"
//
//	// Web search
//	search := vai.VAIWebSearch(exa.NewSearch(os.Getenv("EXA_API_KEY")))
//
//	// Web fetch (content retrieval)
//	fetch := vai.VAIWebFetch(exa.NewContents(os.Getenv("EXA_API_KEY")))
package exa

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
	defaultBaseURL = "https://api.exa.ai"
)

// Option configures an Exa client.
type Option func(*options)

type options struct {
	baseURL    string
	httpClient *http.Client
}

// WithBaseURL overrides the Exa API base URL.
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) { o.httpClient = client }
}

// --- Search Adapter ---

// Search implements vai.WebSearchProvider using the Exa Search API.
type Search struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewSearch creates a new Exa Search provider.
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

// exaSearchRequest is the Exa /search request body.
type exaSearchRequest struct {
	Query          string          `json:"query"`
	NumResults     int             `json:"numResults,omitempty"`
	Contents       *exaContentsOpt `json:"contents,omitempty"`
	IncludeDomains []string        `json:"includeDomains,omitempty"`
	ExcludeDomains []string        `json:"excludeDomains,omitempty"`
	Category       string          `json:"category,omitempty"`
}

// exaContentsOpt configures what content to return with search results.
type exaContentsOpt struct {
	Text       bool              `json:"text,omitempty"`
	Summary    bool              `json:"summary,omitempty"`
	Highlights *exaHighlightsOpt `json:"highlights,omitempty"`
}

// exaHighlightsOpt configures highlight extraction.
type exaHighlightsOpt struct {
	NumSentences int `json:"numSentences,omitempty"`
}

// exaSearchResponse is the Exa /search response body.
type exaSearchResponse struct {
	RequestID string           `json:"requestId"`
	Results   []exaSearchEntry `json:"results"`
}

// exaSearchEntry is a single search result from Exa.
type exaSearchEntry struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate,omitempty"`
	Author        string   `json:"author,omitempty"`
	Text          string   `json:"text,omitempty"`
	Highlights    []string `json:"highlights,omitempty"`
	Summary       string   `json:"summary,omitempty"`
}

// Search implements vai.WebSearchProvider.
func (s *Search) Search(ctx context.Context, query string, opts vai.WebSearchOpts) ([]vai.WebSearchHit, error) {
	reqBody := exaSearchRequest{
		Query:      query,
		NumResults: opts.MaxResults,
		Contents: &exaContentsOpt{
			Text: true,
		},
	}
	if reqBody.NumResults <= 0 {
		reqBody.NumResults = 5
	}
	if len(opts.AllowedDomains) > 0 {
		reqBody.IncludeDomains = opts.AllowedDomains
	}
	if len(opts.BlockedDomains) > 0 {
		reqBody.ExcludeDomains = opts.BlockedDomains
	}
	if opts.Topic != "" {
		reqBody.Category = opts.Topic
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("exa: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("exa: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exa: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("exa: API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var exaResp exaSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&exaResp); err != nil {
		return nil, fmt.Errorf("exa: decode response: %w", err)
	}

	hits := make([]vai.WebSearchHit, 0, len(exaResp.Results))
	for _, r := range exaResp.Results {
		snippet := r.Summary
		if snippet == "" && len(r.Highlights) > 0 {
			snippet = r.Highlights[0]
		}

		// Truncate text content if it's very long for the snippet
		content := r.Text
		if len(content) > 2000 {
			content = content[:2000] + "..."
		}

		hits = append(hits, vai.WebSearchHit{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: snippet,
			Content: content,
		})
	}

	return hits, nil
}

// --- Contents Adapter ---

// Contents implements vai.WebFetchProvider using the Exa Get Contents API.
type Contents struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewContents creates a new Exa Contents provider for URL content extraction.
func NewContents(apiKey string, opts ...Option) *Contents {
	o := &options{
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(o)
	}
	return &Contents{
		apiKey:  apiKey,
		baseURL: o.baseURL,
		client:  o.httpClient,
	}
}

// exaContentsRequest is the Exa /contents request body.
type exaContentsRequest struct {
	IDs  []string       `json:"ids"`
	Text *exaTextConfig `json:"text,omitempty"`
}

// exaTextConfig configures text content retrieval.
type exaTextConfig struct {
	MaxCharacters int `json:"maxCharacters,omitempty"`
}

// exaContentsResponse is the Exa /contents response body.
type exaContentsResponse struct {
	Results []exaContentsEntry `json:"results"`
}

// exaContentsEntry is a single content result from Exa.
type exaContentsEntry struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// Fetch implements vai.WebFetchProvider.
func (c *Contents) Fetch(ctx context.Context, url string, opts vai.WebFetchOpts) (*vai.WebFetchResult, error) {
	reqBody := exaContentsRequest{
		IDs: []string{url},
		Text: &exaTextConfig{
			MaxCharacters: 10000,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("exa: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/contents", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("exa: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exa: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("exa: API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var exaResp exaContentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&exaResp); err != nil {
		return nil, fmt.Errorf("exa: decode response: %w", err)
	}

	if len(exaResp.Results) == 0 {
		return nil, fmt.Errorf("exa: no content retrieved for %s", url)
	}

	r := exaResp.Results[0]
	return &vai.WebFetchResult{
		URL:     r.URL,
		Title:   r.Title,
		Content: r.Text,
	}, nil
}
