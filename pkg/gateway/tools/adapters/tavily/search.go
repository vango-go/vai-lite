package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultBaseURL = "https://api.tavily.com"

type Hit struct {
	Title   string
	URL     string
	Snippet string
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

func (c *Client) Search(ctx context.Context, query string, maxResults int) ([]Hit, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("tavily api key is not configured")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	body, err := json.Marshal(map[string]any{
		"query":        query,
		"search_depth": "basic",
		"max_results":  maxResults,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
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
		return nil, fmt.Errorf("tavily error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var decoded struct {
		Results []struct {
			Title      string `json:"title"`
			URL        string `json:"url"`
			Content    string `json:"content"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	hits := make([]Hit, 0, len(decoded.Results))
	for _, r := range decoded.Results {
		hits = append(hits, Hit{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
			Content: r.RawContent,
		})
	}
	return hits, nil
}
