package tavily

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientExtract_Success(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Fatalf("auth header=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com","title":"Example","raw_content":"content"}]}`))
	}))
	defer ts.Close()

	client := NewClient("key", ts.URL, ts.Client())
	result, err := client.Extract(context.Background(), "https://example.com", "markdown")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if result.URL != "https://example.com" {
		t.Fatalf("url=%q", result.URL)
	}
	if result.Title != "Example" {
		t.Fatalf("title=%q", result.Title)
	}
	if result.Content != "content" {
		t.Fatalf("content=%q", result.Content)
	}
}

func TestClientExtract_Non200(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer ts.Close()

	client := NewClient("key", ts.URL, ts.Client())
	if _, err := client.Extract(context.Background(), "https://example.com", "markdown"); err == nil {
		t.Fatal("expected error")
	}
}

func TestClientExtract_UnsupportedFormat(t *testing.T) {
	t.Parallel()

	client := NewClient("key", "https://api.tavily.com", http.DefaultClient)
	if _, err := client.Extract(context.Background(), "https://example.com", "pdf"); err == nil {
		t.Fatal("expected format validation error")
	}
}

