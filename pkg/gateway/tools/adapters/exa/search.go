package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
)

const defaultBaseURL = "https://api.exa.ai"

type Hit struct {
	Title       string
	URL         string
	Snippet     string
	Content     string
	PublishedAt string
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
		return nil, fmt.Errorf("exa api key is not configured")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	body, err := json.Marshal(map[string]any{
		"query":      query,
		"numResults": maxResults,
		"contents": map[string]any{
			"text": true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := safety.ReadResponseBodyLimited(resp, 8192)
		return nil, fmt.Errorf("exa error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var decoded struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			PublishedDate string   `json:"publishedDate,omitempty"`
			Text          string   `json:"text,omitempty"`
			Highlights    []string `json:"highlights,omitempty"`
			Summary       string   `json:"summary,omitempty"`
		} `json:"results"`
	}
	if err := safety.DecodeJSONBodyLimited(resp, safety.MaxDownloadedBytesV1, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	hits := make([]Hit, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		snippet := strings.TrimSpace(result.Summary)
		if snippet == "" && len(result.Highlights) > 0 {
			snippet = strings.TrimSpace(result.Highlights[0])
		}
		content := strings.TrimSpace(result.Text)
		hits = append(hits, Hit{
			Title:       result.Title,
			URL:         result.URL,
			Snippet:     snippet,
			Content:     content,
			PublishedAt: strings.TrimSpace(result.PublishedDate),
		})
	}
	return hits, nil
}
