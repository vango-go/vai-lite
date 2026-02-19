package stt

import (
	"net/http"
	"testing"
)

func TestNewCartesia_ConstructorsAndName(t *testing.T) {
	client := &http.Client{}
	p := NewCartesiaWithClient("api-key", client)
	if p.httpClient != client {
		t.Fatal("expected custom http client to be set")
	}
	if p.Name() != "cartesia" {
		t.Fatalf("name = %q, want cartesia", p.Name())
	}

	defaultProvider := NewCartesia("api-key")
	if defaultProvider.httpClient == nil {
		t.Fatal("default provider should initialize http client")
	}
}

func TestConvertResponse_MapsLanguageDurationAndWords(t *testing.T) {
	p := &CartesiaProvider{}
	lang := "en"
	duration := 1.5
	input := cartesiaTranscriptionResponse{
		Text:     "hello world",
		Language: &lang,
		Duration: &duration,
		Words: []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		}{
			{Word: "hello", Start: 0.0, End: 0.6},
			{Word: "world", Start: 0.6, End: 1.2},
		},
	}

	out := p.convertResponse(input)
	if out.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", out.Text)
	}
	if out.Language != "en" {
		t.Fatalf("language = %q, want en", out.Language)
	}
	if out.Duration != 1.5 {
		t.Fatalf("duration = %v, want 1.5", out.Duration)
	}
	if len(out.Words) != 2 {
		t.Fatalf("words len = %d, want 2", len(out.Words))
	}
	if out.Words[1].Word != "world" {
		t.Fatalf("words[1].Word = %q, want world", out.Words[1].Word)
	}
}

func TestGetExtensionAndEncoding(t *testing.T) {
	tests := []struct {
		format        string
		wantExtension string
		wantEncoding  string
	}{
		{format: "wav", wantExtension: "wav", wantEncoding: ""},
		{format: "mp3", wantExtension: "mp3", wantEncoding: ""},
		{format: "pcm_s16le", wantExtension: "wav", wantEncoding: "pcm_s16le"},
		{format: "unknown", wantExtension: "wav", wantEncoding: ""},
	}

	for _, tc := range tests {
		if got := getExtension(tc.format); got != tc.wantExtension {
			t.Fatalf("getExtension(%q) = %q, want %q", tc.format, got, tc.wantExtension)
		}
		if got := getEncoding(tc.format); got != tc.wantEncoding {
			t.Fatalf("getEncoding(%q) = %q, want %q", tc.format, got, tc.wantEncoding)
		}
	}
}
