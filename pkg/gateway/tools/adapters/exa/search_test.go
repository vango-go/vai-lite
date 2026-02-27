package exa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSearch_Success(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "exa-key" {
			t.Fatalf("x-api-key=%q", got)
		}
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"https://example.com","summary":"S","text":"C","publishedDate":"2026-02-27"}]}`))
	}))
	defer ts.Close()

	client := NewClient("exa-key", ts.URL, ts.Client())
	hits, err := client.Search(context.Background(), "golang", 8)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("len(hits)=%d", len(hits))
	}
	if hits[0].Snippet != "S" || hits[0].Content != "C" {
		t.Fatalf("hit=%+v", hits[0])
	}
	if gotBody["query"] != "golang" {
		t.Fatalf("query=%v", gotBody["query"])
	}
	if gotBody["numResults"] != float64(8) {
		t.Fatalf("numResults=%v", gotBody["numResults"])
	}
}

func TestClientSearch_Non200(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`upstream exploded`))
	}))
	defer ts.Close()

	client := NewClient("exa-key", ts.URL, ts.Client())
	if _, err := client.Search(context.Background(), "golang", 5); err == nil {
		t.Fatal("expected error")
	}
}
