package firecrawl

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestClientFetch_AuthFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer ts.Close()

	c := NewClient("bad-key", ts.URL, ts.Client())
	if _, err := c.Fetch(context.Background(), "https://example.com"); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestClientFetch_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"x","metadata":{"title":"T"}}}`))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Fetch(ctx, "https://example.com"); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestClientFetch_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{"))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	if _, err := c.Fetch(context.Background(), "https://example.com"); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestClientFetch_NonJSONContentType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"x","metadata":{"title":"T"}}}`))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	if _, err := c.Fetch(context.Background(), "https://example.com"); err == nil {
		t.Fatal("expected content-type error")
	}
}
