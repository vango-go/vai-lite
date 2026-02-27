package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const elevenLabsDefaultWSBase = "wss://api.elevenlabs.io/v1/text-to-speech/{voice_id}/stream-input"

type ElevenLabsProvider struct {
	apiKey     string
	httpClient *http.Client
	wsBaseURL  string
}

func NewElevenLabs(apiKey string) *ElevenLabsProvider {
	return &ElevenLabsProvider{
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{},
		wsBaseURL:  elevenLabsDefaultWSBase,
	}
}

func NewElevenLabsWithClient(apiKey string, client *http.Client) *ElevenLabsProvider {
	if client == nil {
		client = &http.Client{}
	}
	return &ElevenLabsProvider{
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: client,
		wsBaseURL:  elevenLabsDefaultWSBase,
	}
}

func (e *ElevenLabsProvider) WithWSBaseURL(base string) *ElevenLabsProvider {
	if e == nil {
		return e
	}
	base = strings.TrimSpace(base)
	if base != "" {
		e.wsBaseURL = base
	}
	return e
}

func (e *ElevenLabsProvider) Name() string {
	return "elevenlabs"
}

func (e *ElevenLabsProvider) Synthesize(ctx context.Context, text string, opts SynthesizeOptions) (*Synthesis, error) {
	stream, err := e.SynthesizeStream(ctx, text, opts)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var out []byte
	for chunk := range stream.Chunks() {
		out = append(out, chunk...)
	}
	if err := stream.Err(); err != nil && err != io.EOF && err != context.Canceled {
		return nil, err
	}
	return &Synthesis{
		Audio:  out,
		Format: getFormat(opts.Format),
	}, nil
}

func (e *ElevenLabsProvider) SynthesizeStream(ctx context.Context, text string, opts SynthesizeOptions) (*SynthesisStream, error) {
	sc, err := e.NewStreamingContext(ctx, StreamingContextOptions{
		Voice:      opts.Voice,
		Language:   opts.Language,
		Speed:      opts.Speed,
		Volume:     opts.Volume,
		Emotion:    opts.Emotion,
		Format:     opts.Format,
		SampleRate: opts.SampleRate,
	})
	if err != nil {
		return nil, err
	}
	stream := NewSynthesisStream()
	if err := sc.SendText(text, false); err != nil {
		_ = sc.Close()
		return nil, err
	}
	if err := sc.Flush(); err != nil {
		_ = sc.Close()
		return nil, err
	}

	go func() {
		defer stream.FinishSending()
		defer sc.Close()
		for chunk := range sc.Audio() {
			if !stream.Send(chunk) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			stream.SetError(err)
		}
	}()

	return stream, nil
}

func (e *ElevenLabsProvider) NewStreamingContext(ctx context.Context, opts StreamingContextOptions) (*StreamingContext, error) {
	if e == nil || strings.TrimSpace(e.apiKey) == "" {
		return nil, fmt.Errorf("elevenlabs api key is required")
	}
	voiceID := strings.TrimSpace(opts.Voice)
	if voiceID == "" {
		return nil, fmt.Errorf("voice id is required")
	}
	wsURL, err := buildElevenLabsProviderWSURL(e.wsBaseURL, voiceID)
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	header.Set("xi-api-key", e.apiKey)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, err
	}

	sc := NewStreamingContext()
	ctxDone := make(chan struct{})
	var closeOnce sync.Once
	closeConn := func() error {
		var closeErr error
		closeOnce.Do(func() {
			close(ctxDone)
			closeErr = conn.Close()
		})
		return closeErr
	}

	if err := conn.WriteJSON(map[string]any{
		"text":       " ",
		"voice_id":   voiceID,
		"context_id": "ctx_default",
	}); err != nil {
		_ = closeConn()
		return nil, err
	}

	sc.SendFunc = func(text string, isFinal bool) error {
		payload := map[string]any{
			"text": strings.TrimSpace(text),
		}
		if payload["text"] != "" && !strings.HasSuffix(payload["text"].(string), " ") {
			payload["text"] = payload["text"].(string) + " "
		}
		if isFinal {
			payload["flush"] = true
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteJSON(payload)
	}
	sc.CloseFunc = closeConn

	go func() {
		defer sc.FinishAudio()
		defer sc.Close()
		for {
			select {
			case <-ctx.Done():
				sc.SetError(ctx.Err())
				return
			case <-ctxDone:
				return
			default:
			}
			_, data, err := conn.ReadMessage()
			if err != nil {
				sc.SetError(err)
				return
			}
			var msg map[string]json.RawMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			audioB64 := decodeStringRaw(msg["audio"])
			if audioB64 != "" {
				audio, err := base64.StdEncoding.DecodeString(audioB64)
				if err == nil && len(audio) > 0 {
					if !sc.PushAudio(audio) {
						return
					}
				}
			}
			if decodeBoolRaw(msg["isFinal"]) || decodeBoolRaw(msg["is_final"]) {
				return
			}
		}
	}()

	return sc, nil
}

func buildElevenLabsProviderWSURL(base, voiceID string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = elevenLabsDefaultWSBase
	}
	base = strings.ReplaceAll(base, "{voice_id}", url.PathEscape(voiceID))
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid elevenlabs ws url: %w", err)
	}
	if u.Scheme == "" {
		u.Scheme = "wss"
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/text-to-speech/" + url.PathEscape(voiceID) + "/stream-input"
	}
	q := u.Query()
	if q.Get("model_id") == "" {
		q.Set("model_id", "eleven_flash_v2_5")
	}
	if q.Get("output_format") == "" {
		q.Set("output_format", "pcm_24000")
	}
	if q.Get("sync_alignment") == "" {
		q.Set("sync_alignment", "true")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func decodeStringRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func decodeBoolRaw(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var out bool
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	return out
}
