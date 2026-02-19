// Package firecrawl provides Firecrawl API adapters for VAI web fetch tools.
//
// Firecrawl (https://firecrawl.dev) converts web pages into clean markdown
// using AI-powered extraction. It handles JavaScript rendering, anti-bot
// measures, and dynamic content.
//
// Usage:
//
//	import "github.com/vango-go/vai-lite/sdk/adapters/firecrawl"
//
//	fetch := vai.VAIWebFetch(firecrawl.NewScrape(os.Getenv("FIRECRAWL_API_KEY")))
package firecrawl

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
	defaultBaseURL = "https://api.firecrawl.dev"
)

// Option configures a Firecrawl client.
type Option func(*options)

type options struct {
	baseURL    string
	httpClient *http.Client
}

// WithBaseURL overrides the Firecrawl API base URL.
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) { o.httpClient = client }
}

// Scrape implements vai.WebFetchProvider using the Firecrawl Scrape API.
type Scrape struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewScrape creates a new Firecrawl Scrape provider.
func NewScrape(apiKey string, opts ...Option) *Scrape {
	o := &options{
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(o)
	}
	return &Scrape{
		apiKey:  apiKey,
		baseURL: o.baseURL,
		client:  o.httpClient,
	}
}

// firecrawlScrapeRequest is the Firecrawl /v2/scrape request body.
type firecrawlScrapeRequest struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats,omitempty"`
	OnlyMainContent bool     `json:"onlyMainContent,omitempty"`
	Timeout         int      `json:"timeout,omitempty"`
	RemoveBase64    bool     `json:"removeBase64Images,omitempty"`
}

// firecrawlScrapeResponse is the Firecrawl /v2/scrape response body.
type firecrawlScrapeResponse struct {
	Success bool             `json:"success"`
	Data    firecrawlContent `json:"data"`
}

// firecrawlContent contains the scraped page content.
type firecrawlContent struct {
	Markdown string            `json:"markdown,omitempty"`
	HTML     string            `json:"html,omitempty"`
	RawHTML  string            `json:"rawHtml,omitempty"`
	Links    []string          `json:"links,omitempty"`
	Metadata firecrawlMetadata `json:"metadata,omitempty"`
}

// firecrawlMetadata contains metadata about the scraped page.
type firecrawlMetadata struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Language    string `json:"language,omitempty"`
	SourceURL   string `json:"sourceURL,omitempty"`
	StatusCode  int    `json:"statusCode,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Fetch implements vai.WebFetchProvider.
func (s *Scrape) Fetch(ctx context.Context, url string, opts vai.WebFetchOpts) (*vai.WebFetchResult, error) {
	formats := []string{"markdown"}
	if opts.Format == "text" {
		// Firecrawl doesn't have a "text" format; use markdown and let it serve
		formats = []string{"markdown"}
	}

	reqBody := firecrawlScrapeRequest{
		URL:             url,
		Formats:         formats,
		OnlyMainContent: true,
		Timeout:         30000, // 30s
		RemoveBase64:    true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("firecrawl: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/v2/scrape", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("firecrawl: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("firecrawl: API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var fcResp firecrawlScrapeResponse
	if err := json.NewDecoder(resp.Body).Decode(&fcResp); err != nil {
		return nil, fmt.Errorf("firecrawl: decode response: %w", err)
	}

	if !fcResp.Success {
		errMsg := fcResp.Data.Metadata.Error
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return nil, fmt.Errorf("firecrawl: scrape failed: %s", errMsg)
	}

	content := fcResp.Data.Markdown
	if content == "" {
		content = fcResp.Data.HTML
	}
	if content == "" {
		content = fcResp.Data.RawHTML
	}

	return &vai.WebFetchResult{
		URL:     url,
		Title:   fcResp.Data.Metadata.Title,
		Content: content,
	}, nil
}
