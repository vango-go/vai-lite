package tavily

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientSearch_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Fatalf("auth header=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"https://e.com","content":"S","raw_content":"C"}]}`))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	hits, err := c.Search(context.Background(), "golang", 3)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(hits) != 1 || hits[0].Title != "T" {
		t.Fatalf("hits=%+v", hits)
	}
}

func TestClientSearch_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	if _, err := c.Search(context.Background(), "golang", 3); err == nil {
		t.Fatal("expected error")
	}
}

func TestClientSearch_AuthFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer ts.Close()

	c := NewClient("bad-key", ts.URL, ts.Client())
	if _, err := c.Search(context.Background(), "golang", 3); err == nil {
		t.Fatal("expected auth error")
	}
}

func TestClientSearch_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Search(ctx, "golang", 3); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestClientSearch_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{"))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	if _, err := c.Search(context.Background(), "golang", 3); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestClientSearch_NonJSONContentType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer ts.Close()

	c := NewClient("key", ts.URL, ts.Client())
	if _, err := c.Search(context.Background(), "golang", 3); err == nil {
		t.Fatal("expected content-type error")
	}
}
