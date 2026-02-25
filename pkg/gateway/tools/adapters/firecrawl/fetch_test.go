package firecrawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientFetch_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Fatalf("auth header=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"content","metadata":{"title":"Title"}}}`))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	res, err := c.Fetch(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if res.Title != "Title" || res.Content != "content" {
		t.Fatalf("res=%+v", res)
	}
}

func TestClientFetch_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	if _, err := c.Fetch(context.Background(), "https://example.com"); err == nil {
		t.Fatal("expected error")
	}
}
