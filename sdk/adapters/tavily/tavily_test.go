package tavily

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	vai "github.com/vango-go/vai-lite/sdk"
)

func TestSearch_BasicQuery(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("expected /search, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type: application/json")
		}

		// Verify request body
		var reqBody tavilySearchRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if reqBody.Query != "test query" {
			t.Errorf("expected query 'test query', got %q", reqBody.Query)
		}
		if reqBody.MaxResults != 5 {
			t.Errorf("expected max_results 5, got %d", reqBody.MaxResults)
		}

		// Return mock response
		resp := tavilySearchResponse{
			Query: "test query",
			Results: []tavilySearchEntry{
				{
					Title:   "Test Result 1",
					URL:     "https://example.com/1",
					Content: "First result content",
					Score:   0.95,
				},
				{
					Title:   "Test Result 2",
					URL:     "https://example.com/2",
					Content: "Second result content",
					Score:   0.85,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	search := NewSearch("test-key", WithBaseURL(server.URL))
	results, err := search.Search(context.Background(), "test query", vai.WebSearchOpts{
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Test Result 1" {
		t.Errorf("expected title 'Test Result 1', got %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("expected URL 'https://example.com/1', got %q", results[0].URL)
	}
	if results[0].Snippet != "First result content" {
		t.Errorf("expected snippet 'First result content', got %q", results[0].Snippet)
	}
}

func TestSearch_WithDomainFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody tavilySearchRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		if len(reqBody.IncludeDomains) != 1 || reqBody.IncludeDomains[0] != "golang.org" {
			t.Errorf("expected include_domains [golang.org], got %v", reqBody.IncludeDomains)
		}
		if len(reqBody.ExcludeDomains) != 1 || reqBody.ExcludeDomains[0] != "spam.com" {
			t.Errorf("expected exclude_domains [spam.com], got %v", reqBody.ExcludeDomains)
		}

		resp := tavilySearchResponse{Results: []tavilySearchEntry{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	search := NewSearch("test-key", WithBaseURL(server.URL))
	_, err := search.Search(context.Background(), "test", vai.WebSearchOpts{
		AllowedDomains: []string{"golang.org"},
		BlockedDomains: []string{"spam.com"},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
}

func TestSearch_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer server.Close()

	search := NewSearch("bad-key", WithBaseURL(server.URL))
	_, err := search.Search(context.Background(), "test", vai.WebSearchOpts{})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestExtract_BasicFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/extract" {
			t.Errorf("expected /extract, got %s", r.URL.Path)
		}

		var reqBody tavilyExtractRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		if len(reqBody.URLs) != 1 || reqBody.URLs[0] != "https://example.com/article" {
			t.Errorf("expected url [https://example.com/article], got %v", reqBody.URLs)
		}
		if reqBody.Format != "markdown" {
			t.Errorf("expected format 'markdown', got %q", reqBody.Format)
		}

		resp := tavilyExtractResponse{
			Results: []tavilyExtractEntry{
				{
					URL:        "https://example.com/article",
					RawContent: "# Article Title\n\nThis is the article content.",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extract := NewExtract("test-key", WithBaseURL(server.URL))
	result, err := extract.Fetch(context.Background(), "https://example.com/article", vai.WebFetchOpts{
		Format: "markdown",
	})
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if result.URL != "https://example.com/article" {
		t.Errorf("expected URL 'https://example.com/article', got %q", result.URL)
	}
	if result.Content != "# Article Title\n\nThis is the article content." {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestExtract_FailedURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := tavilyExtractResponse{
			Results: []tavilyExtractEntry{},
			FailedResults: []struct {
				URL   string `json:"url"`
				Error string `json:"error"`
			}{
				{URL: "https://blocked.com", Error: "URL blocked"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	extract := NewExtract("test-key", WithBaseURL(server.URL))
	_, err := extract.Fetch(context.Background(), "https://blocked.com", vai.WebFetchOpts{})
	if err == nil {
		t.Fatal("expected error for failed extraction")
	}
}
