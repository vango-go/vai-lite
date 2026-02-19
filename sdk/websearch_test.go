package vai

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

// mockSearchProvider is a test implementation of WebSearchProvider.
type mockSearchProvider struct {
	results []WebSearchHit
	err     error
	// Captured values for assertions
	lastQuery string
	lastOpts  WebSearchOpts
}

func (m *mockSearchProvider) Search(ctx context.Context, query string, opts WebSearchOpts) ([]WebSearchHit, error) {
	m.lastQuery = query
	m.lastOpts = opts
	return m.results, m.err
}

// mockFetchProvider is a test implementation of WebFetchProvider.
type mockFetchProvider struct {
	result *WebFetchResult
	err    error
	// Captured values for assertions
	lastURL  string
	lastOpts WebFetchOpts
}

func (m *mockFetchProvider) Fetch(ctx context.Context, url string, opts WebFetchOpts) (*WebFetchResult, error) {
	m.lastURL = url
	m.lastOpts = opts
	return m.result, m.err
}

func TestVAIWebSearch_BasicUsage(t *testing.T) {
	provider := &mockSearchProvider{
		results: []WebSearchHit{
			{Title: "Go Programming", URL: "https://golang.org", Snippet: "The Go programming language"},
			{Title: "Go by Example", URL: "https://gobyexample.com", Snippet: "Hands-on Go examples"},
		},
	}

	tool := VAIWebSearch(provider)

	// Verify tool type and name
	if tool.Tool.Type != types.ToolTypeFunction {
		t.Errorf("expected type %q, got %q", types.ToolTypeFunction, tool.Tool.Type)
	}
	if tool.Tool.Name != "vai_web_search" {
		t.Errorf("expected name %q, got %q", "vai_web_search", tool.Tool.Name)
	}
	if tool.Handler == nil {
		t.Fatal("expected handler to be non-nil")
	}

	// Execute the handler
	input := `{"query": "golang programming"}`
	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	// Verify provider was called correctly
	if provider.lastQuery != "golang programming" {
		t.Errorf("expected query %q, got %q", "golang programming", provider.lastQuery)
	}

	// Verify result contains expected content
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "Go Programming") {
		t.Errorf("result should contain 'Go Programming', got: %s", resultStr)
	}
	if !strings.Contains(resultStr, "https://golang.org") {
		t.Errorf("result should contain URL, got: %s", resultStr)
	}
}

func TestVAIWebSearch_EmptyQuery(t *testing.T) {
	provider := &mockSearchProvider{}
	tool := VAIWebSearch(provider)

	input := `{"query": ""}`
	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "Error") {
		t.Errorf("expected error message for empty query, got: %s", resultStr)
	}
}

func TestVAIWebSearch_NoResults(t *testing.T) {
	provider := &mockSearchProvider{
		results: []WebSearchHit{},
	}
	tool := VAIWebSearch(provider)

	input := `{"query": "very obscure query"}`
	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "No results found") {
		t.Errorf("expected 'No results found', got: %s", resultStr)
	}
}

func TestVAIWebSearch_CustomConfig(t *testing.T) {
	provider := &mockSearchProvider{
		results: []WebSearchHit{{Title: "Test", URL: "https://test.com", Snippet: "Test result"}},
	}

	tool := VAIWebSearch(provider, VAIWebSearchConfig{
		ToolName:        "custom_search",
		ToolDescription: "Custom search description",
		MaxResults:      10,
		AllowedDomains:  []string{"example.com"},
		BlockedDomains:  []string{"spam.com"},
	})

	// Verify custom name and description
	if tool.Tool.Name != "custom_search" {
		t.Errorf("expected name %q, got %q", "custom_search", tool.Tool.Name)
	}
	if tool.Tool.Description != "Custom search description" {
		t.Errorf("expected custom description, got %q", tool.Tool.Description)
	}

	// Execute and verify options are passed through
	input := `{"query": "test"}`
	_, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if provider.lastOpts.MaxResults != 10 {
		t.Errorf("expected MaxResults 10, got %d", provider.lastOpts.MaxResults)
	}
	if !reflect.DeepEqual(provider.lastOpts.AllowedDomains, []string{"example.com"}) {
		t.Errorf("expected AllowedDomains [example.com], got %v", provider.lastOpts.AllowedDomains)
	}
	if !reflect.DeepEqual(provider.lastOpts.BlockedDomains, []string{"spam.com"}) {
		t.Errorf("expected BlockedDomains [spam.com], got %v", provider.lastOpts.BlockedDomains)
	}
}

func TestVAIWebSearch_ProviderError(t *testing.T) {
	provider := &mockSearchProvider{
		err: context.DeadlineExceeded,
	}
	tool := VAIWebSearch(provider)

	input := `{"query": "test"}`
	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler should not return error (should be in result string): %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "Search error") {
		t.Errorf("expected search error message, got: %s", resultStr)
	}
}

func TestVAIWebSearch_InvalidJSON(t *testing.T) {
	provider := &mockSearchProvider{}
	tool := VAIWebSearch(provider)

	input := `{invalid json}`
	_, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestVAIWebFetch_BasicUsage(t *testing.T) {
	provider := &mockFetchProvider{
		result: &WebFetchResult{
			URL:     "https://example.com/article",
			Title:   "Example Article",
			Content: "# Example\n\nThis is the article content.",
		},
	}

	tool := VAIWebFetch(provider)

	// Verify tool type and name
	if tool.Tool.Type != types.ToolTypeFunction {
		t.Errorf("expected type %q, got %q", types.ToolTypeFunction, tool.Tool.Type)
	}
	if tool.Tool.Name != "vai_web_fetch" {
		t.Errorf("expected name %q, got %q", "vai_web_fetch", tool.Tool.Name)
	}
	if tool.Handler == nil {
		t.Fatal("expected handler to be non-nil")
	}

	// Execute the handler
	input := `{"url": "https://example.com/article"}`
	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	// Verify provider was called correctly
	if provider.lastURL != "https://example.com/article" {
		t.Errorf("expected URL %q, got %q", "https://example.com/article", provider.lastURL)
	}
	if provider.lastOpts.Format != "markdown" {
		t.Errorf("expected format %q, got %q", "markdown", provider.lastOpts.Format)
	}

	// Verify result contains expected content
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "Example Article") {
		t.Errorf("result should contain title, got: %s", resultStr)
	}
	if !strings.Contains(resultStr, "This is the article content") {
		t.Errorf("result should contain content, got: %s", resultStr)
	}
}

func TestVAIWebFetch_EmptyURL(t *testing.T) {
	provider := &mockFetchProvider{}
	tool := VAIWebFetch(provider)

	input := `{"url": ""}`
	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "Error") {
		t.Errorf("expected error message for empty URL, got: %s", resultStr)
	}
}

func TestVAIWebFetch_CustomConfig(t *testing.T) {
	provider := &mockFetchProvider{
		result: &WebFetchResult{
			URL:     "https://example.com",
			Content: "Plain text content",
		},
	}

	tool := VAIWebFetch(provider, VAIWebFetchConfig{
		ToolName:        "custom_fetch",
		ToolDescription: "Custom fetch description",
		Format:          "text",
	})

	// Verify custom name
	if tool.Tool.Name != "custom_fetch" {
		t.Errorf("expected name %q, got %q", "custom_fetch", tool.Tool.Name)
	}

	// Execute and verify format option
	input := `{"url": "https://example.com"}`
	_, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if provider.lastOpts.Format != "text" {
		t.Errorf("expected format %q, got %q", "text", provider.lastOpts.Format)
	}
}

func TestVAIWebFetch_ProviderError(t *testing.T) {
	provider := &mockFetchProvider{
		err: context.DeadlineExceeded,
	}
	tool := VAIWebFetch(provider)

	input := `{"url": "https://example.com"}`
	result, err := tool.Handler(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("handler should not return error: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.Contains(resultStr, "Fetch error") {
		t.Errorf("expected fetch error message, got: %s", resultStr)
	}
}

func TestWebFetch_NativeTool(t *testing.T) {
	// Test the native WebFetch() constructor (not VAI version)
	tool := WebFetch()
	if tool.Type != types.ToolTypeWebFetch {
		t.Errorf("expected type %q, got %q", types.ToolTypeWebFetch, tool.Type)
	}
	// No config passed — Config will be typed nil (*WebFetchConfig)(nil)
	if cfg, ok := tool.Config.(*WebFetchConfig); ok && cfg != nil {
		t.Errorf("expected nil config pointer, got %v", cfg)
	}

	// With config
	tool = WebFetch(WebFetchConfig{
		MaxUses:        5,
		AllowedDomains: []string{"example.com"},
	})
	if tool.Type != types.ToolTypeWebFetch {
		t.Errorf("expected type %q, got %q", types.ToolTypeWebFetch, tool.Type)
	}
	cfg, ok := tool.Config.(*WebFetchConfig)
	if !ok {
		t.Fatalf("expected *WebFetchConfig, got %T", tool.Config)
	}
	if cfg.MaxUses != 5 {
		t.Errorf("expected MaxUses 5, got %d", cfg.MaxUses)
	}
}

func TestVAIWebSearch_SchemaGeneration(t *testing.T) {
	provider := &mockSearchProvider{}
	tool := VAIWebSearch(provider)

	if tool.Tool.InputSchema == nil {
		t.Fatal("expected InputSchema to be non-nil")
	}
	schema := tool.Tool.InputSchema
	if schema.Type != "object" {
		t.Errorf("expected schema type 'object', got %q", schema.Type)
	}
	if _, ok := schema.Properties["query"]; !ok {
		t.Error("expected 'query' property in schema")
	}
}

func TestVAIWebFetch_SchemaGeneration(t *testing.T) {
	provider := &mockFetchProvider{}
	tool := VAIWebFetch(provider)

	if tool.Tool.InputSchema == nil {
		t.Fatal("expected InputSchema to be non-nil")
	}
	schema := tool.Tool.InputSchema
	if schema.Type != "object" {
		t.Errorf("expected schema type 'object', got %q", schema.Type)
	}
	if _, ok := schema.Properties["url"]; !ok {
		t.Error("expected 'url' property in schema")
	}
}
