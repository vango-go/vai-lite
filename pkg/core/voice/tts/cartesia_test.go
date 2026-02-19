package tts

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

func TestBuildOutputFormat(t *testing.T) {
	p := &CartesiaProvider{}

	mp3 := p.buildOutputFormat(SynthesizeOptions{Format: "mp3", SampleRate: 44100})
	if mp3.Container != "mp3" || mp3.SampleRate != 44100 || mp3.BitRate == 0 {
		t.Fatalf("mp3 format = %#v, want mp3/44100/non-zero bitrate", mp3)
	}

	pcm := p.buildOutputFormat(SynthesizeOptions{Format: "pcm", SampleRate: 16000})
	if pcm.Container != "raw" || pcm.Encoding != "pcm_s16le" || pcm.SampleRate != 16000 {
		t.Fatalf("pcm format = %#v, want raw/pcm_s16le/16000", pcm)
	}

	wavDefault := p.buildOutputFormat(SynthesizeOptions{})
	if wavDefault.Container != "wav" || wavDefault.Encoding != "pcm_s16le" || wavDefault.SampleRate != 24000 {
		t.Fatalf("default format = %#v, want wav/pcm_s16le/24000", wavDefault)
	}
}

func TestBuildStreamingOutputFormat(t *testing.T) {
	got := buildStreamingOutputFormat(0)
	if got.Container != "raw" || got.Encoding != "pcm_s16le" || got.SampleRate != 24000 {
		t.Fatalf("default streaming format = %#v, want raw/pcm_s16le/24000", got)
	}

	got = buildStreamingOutputFormat(48000)
	if got.SampleRate != 48000 {
		t.Fatalf("streaming sample rate = %d, want 48000", got.SampleRate)
	}
}

func TestGetFormat(t *testing.T) {
	if got := getFormat("mp3"); got != "mp3" {
		t.Fatalf("getFormat(mp3) = %q, want mp3", got)
	}
	if got := getFormat("unknown"); got != "wav" {
		t.Fatalf("getFormat(unknown) = %q, want wav", got)
	}
}
