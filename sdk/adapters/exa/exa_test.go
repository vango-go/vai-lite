package exa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	vai "github.com/vango-go/vai-lite/sdk"
)

func TestSearch_BasicQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("expected /search, got %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key header")
		}

		var reqBody exaSearchRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody.Query != "latest AI research" {
			t.Errorf("expected query 'latest AI research', got %q", reqBody.Query)
		}
		if reqBody.NumResults != 5 {
			t.Errorf("expected numResults 5, got %d", reqBody.NumResults)
		}
		if reqBody.Contents == nil || !reqBody.Contents.Text {
			t.Error("expected contents.text to be true")
		}

		resp := exaSearchResponse{
			RequestID: "test-request",
			Results: []exaSearchEntry{
				{
					Title:      "LLM Research Paper",
					URL:        "https://arxiv.org/paper123",
					Text:       "Abstract: This paper explores...",
					Highlights: []string{"Key finding about LLMs"},
					Summary:    "A comprehensive overview of LLM research",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	search := NewSearch("test-key", WithBaseURL(server.URL))
	results, err := search.Search(context.Background(), "latest AI research", vai.WebSearchOpts{
		MaxResults: 5,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "LLM Research Paper" {
		t.Errorf("expected title 'LLM Research Paper', got %q", results[0].Title)
	}
	if results[0].URL != "https://arxiv.org/paper123" {
		t.Errorf("expected URL, got %q", results[0].URL)
	}
	if results[0].Snippet != "A comprehensive overview of LLM research" {
		t.Errorf("expected summary as snippet, got %q", results[0].Snippet)
	}
	if results[0].Content != "Abstract: This paper explores..." {
		t.Errorf("expected text content, got %q", results[0].Content)
	}
}

func TestSearch_FallbackToHighlights(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := exaSearchResponse{
			Results: []exaSearchEntry{
				{
					Title:      "Test",
					URL:        "https://test.com",
					Highlights: []string{"Highlighted text"},
					// No summary
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	search := NewSearch("test-key", WithBaseURL(server.URL))
	results, err := search.Search(context.Background(), "test", vai.WebSearchOpts{})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if results[0].Snippet != "Highlighted text" {
		t.Errorf("expected highlight as snippet fallback, got %q", results[0].Snippet)
	}
}

func TestSearch_WithDomainFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody exaSearchRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		if len(reqBody.IncludeDomains) != 1 || reqBody.IncludeDomains[0] != "arxiv.org" {
			t.Errorf("expected includeDomains [arxiv.org], got %v", reqBody.IncludeDomains)
		}
		if len(reqBody.ExcludeDomains) != 1 || reqBody.ExcludeDomains[0] != "blog.com" {
			t.Errorf("expected excludeDomains [blog.com], got %v", reqBody.ExcludeDomains)
		}

		resp := exaSearchResponse{Results: []exaSearchEntry{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	search := NewSearch("test-key", WithBaseURL(server.URL))
	_, err := search.Search(context.Background(), "test", vai.WebSearchOpts{
		AllowedDomains: []string{"arxiv.org"},
		BlockedDomains: []string{"blog.com"},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
}

func TestContents_BasicFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/contents" {
			t.Errorf("expected /contents, got %s", r.URL.Path)
		}

		var reqBody exaContentsRequest
		json.NewDecoder(r.Body).Decode(&reqBody)

		if len(reqBody.IDs) != 1 || reqBody.IDs[0] != "https://example.com/article" {
			t.Errorf("expected ids [https://example.com/article], got %v", reqBody.IDs)
		}

		resp := exaContentsResponse{
			Results: []exaContentsEntry{
				{
					URL:   "https://example.com/article",
					Title: "Example Article",
					Text:  "This is the full article text content.",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	contents := NewContents("test-key", WithBaseURL(server.URL))
	result, err := contents.Fetch(context.Background(), "https://example.com/article", vai.WebFetchOpts{})
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	if result.URL != "https://example.com/article" {
		t.Errorf("expected URL, got %q", result.URL)
	}
	if result.Title != "Example Article" {
		t.Errorf("expected title 'Example Article', got %q", result.Title)
	}
	if result.Content != "This is the full article text content." {
		t.Errorf("expected text content, got %q", result.Content)
	}
}

func TestContents_NoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := exaContentsResponse{Results: []exaContentsEntry{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	contents := NewContents("test-key", WithBaseURL(server.URL))
	_, err := contents.Fetch(context.Background(), "https://nonexistent.com", vai.WebFetchOpts{})
	if err == nil {
		t.Fatal("expected error for no results")
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
