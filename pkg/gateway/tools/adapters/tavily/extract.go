package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
)

type ExtractResult struct {
	URL     string
	Title   string
	Content string
}

func (c *Client) Extract(ctx context.Context, targetURL, format string) (*ExtractResult, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("tavily api key is not configured")
	}
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "", "markdown", "text", "html":
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
	if format == "" {
		format = "markdown"
	}

	body, err := json.Marshal(map[string]any{
		"urls":          []string{targetURL},
		"extract_depth": "basic",
		"format":        format,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/extract", bytes.NewReader(body))
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
		b, _ := safety.ReadResponseBodyLimited(resp, 8192)
		return nil, fmt.Errorf("tavily error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var decoded struct {
		Results []struct {
			URL        string `json:"url"`
			Title      string `json:"title,omitempty"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
		FailedResults []struct {
			URL   string `json:"url"`
			Error string `json:"error"`
		} `json:"failed_results"`
	}
	if err := safety.DecodeJSONBodyLimited(resp, safety.MaxDownloadedBytesV1, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(decoded.Results) == 0 {
		if len(decoded.FailedResults) > 0 {
			msg := strings.TrimSpace(decoded.FailedResults[0].Error)
			if msg == "" {
				msg = "extract failed"
			}
			return nil, fmt.Errorf("tavily: %s", msg)
		}
		return nil, fmt.Errorf("tavily: no extract result returned")
	}

	first := decoded.Results[0]
	return &ExtractResult{
		URL:     strings.TrimSpace(first.URL),
		Title:   strings.TrimSpace(first.Title),
		Content: first.RawContent,
	}, nil
}

