package firecrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultBaseURL = "https://api.firecrawl.dev"

type Result struct {
	URL     string
	Title   string
	Content string
}

type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewClient(apiKey, baseURL string, httpClient *http.Client) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (c *Client) Configured() bool {
	return c != nil && strings.TrimSpace(c.apiKey) != ""
}

func (c *Client) Fetch(ctx context.Context, targetURL string) (*Result, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("firecrawl api key is not configured")
	}
	if strings.TrimSpace(targetURL) == "" {
		return nil, fmt.Errorf("url is required")
	}

	body, err := json.Marshal(map[string]any{
		"url":             targetURL,
		"formats":         []string{"markdown"},
		"onlyMainContent": true,
		"timeout":         30000,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/scrape", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, fmt.Errorf("firecrawl error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var decoded struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
			HTML     string `json:"html"`
			RawHTML  string `json:"rawHtml"`
			Metadata struct {
				Title string `json:"title"`
				Error string `json:"error"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !decoded.Success {
		msg := strings.TrimSpace(decoded.Data.Metadata.Error)
		if msg == "" {
			msg = "scrape failed"
		}
		return nil, fmt.Errorf("firecrawl: %s", msg)
	}

	content := decoded.Data.Markdown
	if content == "" {
		content = decoded.Data.HTML
	}
	if content == "" {
		content = decoded.Data.RawHTML
	}

	return &Result{URL: targetURL, Title: decoded.Data.Metadata.Title, Content: content}, nil
}
