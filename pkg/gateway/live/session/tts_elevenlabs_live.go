package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

const defaultElevenLabsWSBase = "wss://api.elevenlabs.io/v1/text-to-speech/{voice_id}/stream-input"

type elevenLabsLiveConfig struct {
	APIKey     string
	VoiceID    string
	BaseWSURL  string
	AudioOutHz int
}

type elevenLabsLiveChunk struct {
	ContextID string
	Audio     []byte
	Alignment *protocol.Alignment
	Final     bool
}

type elevenLabsLiveConn struct {
	conn *websocket.Conn

	writeMu sync.Mutex
	metaMu  sync.Mutex

	activeContextID string
	chunks          chan elevenLabsLiveChunk
	closed          chan struct{}
	closeOnce       sync.Once
}

func newElevenLabsLiveConn(ctx context.Context, cfg elevenLabsLiveConfig) (*elevenLabsLiveConn, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("elevenlabs api key is required")
	}
	if strings.TrimSpace(cfg.VoiceID) == "" {
		return nil, fmt.Errorf("elevenlabs voice id is required")
	}
	wsURL, err := buildElevenLabsWSURL(strings.TrimSpace(cfg.BaseWSURL), strings.TrimSpace(cfg.VoiceID))
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("xi-api-key", strings.TrimSpace(cfg.APIKey))

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, err
	}
	out := &elevenLabsLiveConn{
		conn:   conn,
		chunks: make(chan elevenLabsLiveChunk, 256),
		closed: make(chan struct{}),
	}

	go out.readLoop()
	go out.keepAliveLoop()
	return out, nil
}

func (c *elevenLabsLiveConn) StartContext(ctx context.Context, contextID string) error {
	contextID = strings.TrimSpace(contextID)
	if contextID == "" {
		return fmt.Errorf("context id is required")
	}
	c.metaMu.Lock()
	c.activeContextID = contextID
	c.metaMu.Unlock()
	return c.writeJSON(ctx, map[string]any{
		"text":       " ",
		"context_id": contextID,
	})
}

func (c *elevenLabsLiveConn) SendText(ctx context.Context, contextID, text string, flush bool) error {
	contextID = strings.TrimSpace(contextID)
	if contextID == "" {
		return fmt.Errorf("context id is required")
	}
	payloadText := text
	if strings.TrimSpace(payloadText) != "" && !strings.HasSuffix(payloadText, " ") {
		payloadText += " "
	}
	msg := map[string]any{
		"text":       payloadText,
		"context_id": contextID,
	}
	if flush {
		msg["flush"] = true
	}
	return c.writeJSON(ctx, msg)
}

func (c *elevenLabsLiveConn) CloseContext(ctx context.Context, contextID string) error {
	contextID = strings.TrimSpace(contextID)
	if contextID == "" {
		return nil
	}
	c.metaMu.Lock()
	if c.activeContextID == contextID {
		c.activeContextID = ""
	}
	c.metaMu.Unlock()
	return c.writeJSON(ctx, map[string]any{
		"context_id":    contextID,
		"close_context": true,
	})
}

func (c *elevenLabsLiveConn) Chunks() <-chan elevenLabsLiveChunk {
	if c == nil {
		ch := make(chan elevenLabsLiveChunk)
		close(ch)
		return ch
	}
	return c.chunks
}

func (c *elevenLabsLiveConn) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
	return nil
}

func (c *elevenLabsLiveConn) readLoop() {
	defer close(c.chunks)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var msg map[string]json.RawMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		contextID := decodeString(msg["context_id"])
		if contextID == "" {
			contextID = decodeString(msg["contextId"])
		}

		audioB64 := decodeString(msg["audio"])
		var audio []byte
		if audioB64 != "" {
			audio, _ = base64.StdEncoding.DecodeString(audioB64)
		}
		final := decodeBool(msg["isFinal"]) || decodeBool(msg["is_final"])
		alignment := parseElevenLabsAlignment(msg)

		if len(audio) == 0 && !final {
			continue
		}

		select {
		case c.chunks <- elevenLabsLiveChunk{
			ContextID: contextID,
			Audio:     audio,
			Alignment: alignment,
			Final:     final,
		}:
		case <-c.closed:
			return
		}
	}
}

func (c *elevenLabsLiveConn) keepAliveLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-ticker.C:
			c.metaMu.Lock()
			contextID := c.activeContextID
			c.metaMu.Unlock()
			if contextID == "" {
				continue
			}
			_ = c.writeJSON(context.Background(), map[string]any{
				"text":       "",
				"context_id": contextID,
			})
		}
	}
}

func (c *elevenLabsLiveConn) writeJSON(ctx context.Context, payload any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	} else {
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	}
	return c.conn.WriteJSON(payload)
}

func buildElevenLabsWSURL(base, voiceID string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = defaultElevenLabsWSBase
	}
	base = strings.ReplaceAll(base, "{voice_id}", url.PathEscape(voiceID))
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid elevenlabs ws base url: %w", err)
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
	if q.Get("apply_text_normalization") == "" {
		q.Set("apply_text_normalization", "off")
	}
	if q.Get("inactivity_timeout") == "" {
		q.Set("inactivity_timeout", "60")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func parseElevenLabsAlignment(msg map[string]json.RawMessage) *protocol.Alignment {
	raw := msg["normalizedAlignment"]
	if len(raw) == 0 {
		raw = msg["normalized_alignment"]
	}
	if len(raw) == 0 {
		raw = msg["alignment"]
	}
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		Chars            []string `json:"chars"`
		CharStartTimesMS []int    `json:"charStartTimesMs"`
		CharDurationsMS  []int    `json:"charDurationsMs"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	if len(payload.Chars) == 0 || len(payload.Chars) != len(payload.CharStartTimesMS) || len(payload.Chars) != len(payload.CharDurationsMS) {
		return nil
	}
	return &protocol.Alignment{
		Kind:        protocol.AlignmentKindChar,
		Normalized:  true,
		Chars:       payload.Chars,
		CharStartMS: payload.CharStartTimesMS,
		CharDurMS:   payload.CharDurationsMS,
	}
}

func decodeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func decodeBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var out bool
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	return out
}
