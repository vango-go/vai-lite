package tavily

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
