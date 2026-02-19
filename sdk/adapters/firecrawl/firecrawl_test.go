package firecrawl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	vai "github.com/vango-go/vai-lite/sdk"
)

func TestScrape_BasicFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v2/scrape" {
			t.Errorf("expected /v2/scrape, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization header")
		}

		var reqBody firecrawlScrapeRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody.URL != "https://example.com" {
			t.Errorf("expected URL 'https://example.com', got %q", reqBody.URL)
		}
		if !reqBody.OnlyMainContent {
			t.Error("expected OnlyMainContent to be true")
		}

		resp := firecrawlScrapeResponse{
			Success: true,
			Data: firecrawlContent{
				Markdown: "# Example Page\n\nThis is the page content.",
				Metadata: firecrawlMetadata{
					Title:     "Example Page",
					SourceURL: "https://example.com",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	scrape := NewScrape("test-key", WithBaseURL(server.URL))
	result, err := scrape.Fetch(context.Background(), "https://example.com", vai.WebFetchOpts{})
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if result.URL != "https://example.com" {
		t.Errorf("expected URL 'https://example.com', got %q", result.URL)
	}
	if result.Title != "Example Page" {
		t.Errorf("expected title 'Example Page', got %q", result.Title)
	}
	if result.Content != "# Example Page\n\nThis is the page content." {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestScrape_FailedScrape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := firecrawlScrapeResponse{
			Success: false,
			Data: firecrawlContent{
				Metadata: firecrawlMetadata{
					Error: "page not found",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	scrape := NewScrape("test-key", WithBaseURL(server.URL))
	_, err := scrape.Fetch(context.Background(), "https://example.com/404", vai.WebFetchOpts{})
	if err == nil {
		t.Fatal("expected error for failed scrape")
	}
}

func TestScrape_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer server.Close()

	scrape := NewScrape("bad-key", WithBaseURL(server.URL))
	_, err := scrape.Fetch(context.Background(), "https://example.com", vai.WebFetchOpts{})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestScrape_FallbackToHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := firecrawlScrapeResponse{
			Success: true,
			Data: firecrawlContent{
				Markdown: "", // Empty markdown
				HTML:     "<h1>Example</h1><p>Content</p>",
				Metadata: firecrawlMetadata{
					Title: "Example",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	scrape := NewScrape("test-key", WithBaseURL(server.URL))
	result, err := scrape.Fetch(context.Background(), "https://example.com", vai.WebFetchOpts{})
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if result.Content != "<h1>Example</h1><p>Content</p>" {
		t.Errorf("expected HTML fallback content, got %q", result.Content)
	}
}
