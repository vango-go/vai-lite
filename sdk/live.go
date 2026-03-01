package vai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	liveproto "github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
)

const (
	defaultLiveConnectTimeout = 15 * time.Second
)

// LiveService provides access to the gateway Live WebSocket API (/v1/live).
//
// Live mode is gateway-owned orchestration (STT + endpointing + tool loop + TTS),
// so this service is only available in proxy mode.
type LiveService struct {
	client *Client
}

// LiveVoice configures voice settings for a Live session.
type LiveVoice struct {
	Provider string
	VoiceID  string
	Language string
	Speed    float64
	Volume   float64
	Emotion  string
}

// LiveFeatures controls optional Live protocol behavior.
type LiveFeatures struct {
	AudioTransport         string
	SendPlaybackMarks      bool
	WantPartialTranscripts bool
	WantAssistantText      bool
	ClientHasAEC           bool
	WantRunEvents          bool
}

// LiveTools configures gateway server tools and client callback tools.
type LiveTools struct {
	ServerTools      []string
	ServerToolConfig map[string]any
	ClientTools      []types.Tool
}

// LiveConnectRequest configures a low-level Live WebSocket session.
type LiveConnectRequest struct {
	Model    string
	System   string
	Messages []types.Message
	Voice    LiveVoice
	Features LiveFeatures
	Tools    LiveTools

	// Optional BYOK overrides/additional keys (provider -> key).
	// Keys are lowercase provider names (for example: anthropic, openai, tavily, cartesia).
	BYOK map[string]string
}

// LiveRunRequest provides a RunStream-like configuration for live mode.
// The gateway still owns the loop and audio orchestration.
type LiveRunRequest struct {
	Model            string
	System           string
	Messages         []types.Message
	Voice            LiveVoice
	Features         LiveFeatures
	ServerTools      []string
	ServerToolConfig map[string]any

	// Optional client function-tool definitions.
	// Function tools from WithTools(...) are merged with these.
	Tools []types.Tool
}

// LiveAudioMeta carries optional metadata for outbound audio frames.
type LiveAudioMeta struct {
	Seq         int64
	TimestampMS *int64
}

// LivePlaybackMark is reported by the client to support played-history truncation.
type LivePlaybackMark struct {
	AssistantAudioID string
	PlayedMS         int64
	BufferedMS       int64
	State            string
	TimestampMS      *int64
}

// LiveToolResult is a client tool callback result sent back to the gateway.
type LiveToolResult struct {
	TurnID  int
	ID      string
	Content []types.ContentBlock
	IsError bool
	Error   *types.Error
}

// LiveEvent is a low-level event emitted by LiveSession.Events().
type LiveEvent interface {
	liveEventType() string
}

type LiveHelloAckEvent struct{ Ack liveproto.ServerHelloAck }

func (e LiveHelloAckEvent) liveEventType() string { return "hello_ack" }

type LiveWarningEvent struct{ Warning liveproto.ServerWarning }

func (e LiveWarningEvent) liveEventType() string { return "warning" }

type LiveErrorEvent struct{ Error liveproto.ServerError }

func (e LiveErrorEvent) liveEventType() string { return "error" }

type LiveAudioInAckEvent struct{ Ack liveproto.ServerAudioInAck }

func (e LiveAudioInAckEvent) liveEventType() string { return "audio_in_ack" }

// LiveTranscriptDeltaEvent carries streaming transcript deltas from STT.
type LiveTranscriptDeltaEvent struct {
	UtteranceID string
	IsFinal     bool
	Text        string
	Stability   *float64
	TimestampMS int64
}

func (e LiveTranscriptDeltaEvent) liveEventType() string      { return "transcript_delta" }
func (e LiveTranscriptDeltaEvent) runStreamEventType() string { return "live_transcript_delta" }

// LiveUtteranceFinalEvent marks a committed user utterance.
type LiveUtteranceFinalEvent struct {
	UtteranceID string
	Text        string
	EndMS       int64
}

func (e LiveUtteranceFinalEvent) liveEventType() string      { return "utterance_final" }
func (e LiveUtteranceFinalEvent) runStreamEventType() string { return "live_utterance_final" }

type LiveAssistantAudioStartEvent struct {
	Start liveproto.ServerAssistantAudioStart
}

func (e LiveAssistantAudioStartEvent) liveEventType() string { return "assistant_audio_start" }

type LiveAssistantAudioChunkEvent struct {
	AssistantAudioID string
	Seq              int64
	Data             []byte
	Format           string
	Alignment        *liveproto.Alignment
}

func (e LiveAssistantAudioChunkEvent) liveEventType() string { return "assistant_audio_chunk" }

type LiveAssistantAudioEndEvent struct {
	End liveproto.ServerAssistantAudioEnd
}

func (e LiveAssistantAudioEndEvent) liveEventType() string { return "assistant_audio_end" }

type LiveAssistantTextDeltaEvent struct {
	AssistantAudioID string
	Delta            string
}

func (e LiveAssistantTextDeltaEvent) liveEventType() string { return "assistant_text_delta" }

type LiveAssistantTextFinalEvent struct {
	AssistantAudioID string
	Text             string
}

func (e LiveAssistantTextFinalEvent) liveEventType() string { return "assistant_text_final" }

// LiveAudioResetEvent signals clients to flush playback immediately.
type LiveAudioResetEvent struct {
	Reason           string
	AssistantAudioID string
}

func (e LiveAudioResetEvent) liveEventType() string      { return "audio_reset" }
func (e LiveAudioResetEvent) runStreamEventType() string { return "live_audio_reset" }

type LiveRunEvent struct {
	TurnID int
	Event  types.RunStreamEvent
}

func (e LiveRunEvent) liveEventType() string { return "run_event" }

type LiveToolCallEvent struct {
	TurnID int
	ID     string
	Name   string
	Input  map[string]any
}

func (e LiveToolCallEvent) liveEventType() string { return "tool_call" }

type LiveToolCancelEvent struct {
	TurnID int
	ID     string
	Reason string
}

func (e LiveToolCancelEvent) liveEventType() string { return "tool_cancel" }

type LiveUnknownEvent struct {
	Type string
	Raw  json.RawMessage
}

func (e LiveUnknownEvent) liveEventType() string { return e.Type }

// LiveSession is a low-level live websocket session.
type LiveSession struct {
	conn *websocket.Conn

	events    chan LiveEvent
	runEvents chan types.RunStreamEvent
	done      chan struct{}

	writeMu   sync.Mutex
	closeOnce sync.Once
	closed    atomic.Bool

	errMu sync.Mutex
	err   error
}

// Events yields low-level live websocket events.
func (s *LiveSession) Events() <-chan LiveEvent {
	if s == nil {
		return nil
	}
	return s.events
}

// RunEvents yields decoded run_event payloads from the live session.
func (s *LiveSession) RunEvents() <-chan types.RunStreamEvent {
	if s == nil {
		return nil
	}
	return s.runEvents
}

// SendAudioFrame sends a base64_json audio_frame to the gateway.
func (s *LiveSession) SendAudioFrame(pcm []byte, meta LiveAudioMeta) error {
	if s == nil {
		return fmt.Errorf("session must not be nil")
	}
	frame := liveproto.ClientAudioFrame{
		Type:        "audio_frame",
		Seq:         meta.Seq,
		TimestampMS: meta.TimestampMS,
		DataB64:     base64.StdEncoding.EncodeToString(pcm),
	}
	return s.sendJSON(frame)
}

// SendPlaybackMark reports playback progress for interruption/truncation accuracy.
func (s *LiveSession) SendPlaybackMark(mark LivePlaybackMark) error {
	if s == nil {
		return fmt.Errorf("session must not be nil")
	}
	msg := liveproto.ClientPlaybackMark{
		Type:             "playback_mark",
		AssistantAudioID: mark.AssistantAudioID,
		PlayedMS:         mark.PlayedMS,
		BufferedMS:       mark.BufferedMS,
		State:            mark.State,
		TimestampMS:      mark.TimestampMS,
	}
	return s.sendJSON(msg)
}

// SendToolResult sends a client tool callback result.
func (s *LiveSession) SendToolResult(result LiveToolResult) error {
	if s == nil {
		return fmt.Errorf("session must not be nil")
	}
	msg := liveproto.ClientToolResult{
		Type:    "tool_result",
		TurnID:  result.TurnID,
		ID:      strings.TrimSpace(result.ID),
		Content: result.Content,
		IsError: result.IsError,
		Error:   result.Error,
	}
	return s.sendJSON(msg)
}

// Interrupt requests a turn interruption.
func (s *LiveSession) Interrupt() error {
	return s.sendControl("interrupt")
}

// CancelTurn cancels the active model/tool turn.
func (s *LiveSession) CancelTurn() error {
	return s.sendControl("cancel_turn")
}

// EndSession requests a graceful live session shutdown.
func (s *LiveSession) EndSession() error {
	return s.sendControl("end_session")
}

func (s *LiveSession) sendControl(op string) error {
	if s == nil {
		return fmt.Errorf("session must not be nil")
	}
	return s.sendJSON(liveproto.ClientControl{Type: "control", Op: strings.TrimSpace(op)})
}

func (s *LiveSession) sendJSON(v any) error {
	if s == nil {
		return fmt.Errorf("session must not be nil")
	}
	if s.closed.Load() {
		return fmt.Errorf("live session is closed")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteJSON(v)
}

// Close closes the websocket session.
func (s *LiveSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.writeMu.Lock()
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(2*time.Second))
		s.writeMu.Unlock()
		_ = s.conn.Close()
	})
	<-s.done
	return nil
}

// Err returns the terminal session error (if any).
func (s *LiveSession) Err() error {
	if s == nil {
		return nil
	}
	<-s.done
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *LiveSession) setErr(err error) {
	if err == nil {
		return
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *LiveSession) readLoop() {
	defer close(s.done)
	defer close(s.events)
	defer close(s.runEvents)

	assistantFormat := make(map[string]string)
	var pendingBinaryHeader *liveproto.ServerAssistantAudioChunkHeader

	for {
		messageType, data, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			s.setErr(err)
			return
		}

		switch messageType {
		case websocket.TextMessage:
			event, runEvent, frameErr := decodeLiveTextFrame(data, assistantFormat, &pendingBinaryHeader)
			if frameErr != nil {
				s.setErr(frameErr)
				return
			}
			if event != nil {
				s.emitEvent(event)
				if errEvent, ok := event.(LiveErrorEvent); ok {
					s.setErr(core.NewAPIError(strings.TrimSpace(errEvent.Error.Message)))
				}
			}
			if runEvent != nil {
				s.emitRunEvent(runEvent)
			}
		case websocket.BinaryMessage:
			if pendingBinaryHeader == nil {
				continue
			}
			chunk := LiveAssistantAudioChunkEvent{
				AssistantAudioID: pendingBinaryHeader.AssistantAudioID,
				Seq:              pendingBinaryHeader.Seq,
				Data:             append([]byte(nil), data...),
				Format:           "pcm_s16le",
				Alignment:        pendingBinaryHeader.Alignment,
			}
			pendingBinaryHeader = nil
			s.emitEvent(chunk)
		default:
			continue
		}
	}
}

func (s *LiveSession) emitEvent(event LiveEvent) {
	if event == nil {
		return
	}
	select {
	case s.events <- event:
	default:
		// Avoid deadlocking read loop if caller stops consuming.
	}
}

func (s *LiveSession) emitRunEvent(event types.RunStreamEvent) {
	if event == nil {
		return
	}
	select {
	case s.runEvents <- event:
	default:
		// Best effort channel; run events are still available via Events() as LiveRunEvent.
	}
	s.emitEvent(LiveRunEvent{Event: event})
}

func decodeLiveTextFrame(data []byte, assistantFormat map[string]string, pendingBinaryHeader **liveproto.ServerAssistantAudioChunkHeader) (LiveEvent, types.RunStreamEvent, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, nil, fmt.Errorf("decode live frame envelope: %w", err)
	}
	typ := strings.TrimSpace(envelope.Type)
	if typ == "" {
		return nil, nil, fmt.Errorf("live frame missing type")
	}

	switch typ {
	case "hello_ack":
		var ack liveproto.ServerHelloAck
		if err := json.Unmarshal(data, &ack); err != nil {
			return nil, nil, fmt.Errorf("decode hello_ack: %w", err)
		}
		return LiveHelloAckEvent{Ack: ack}, nil, nil
	case "warning":
		var warning liveproto.ServerWarning
		if err := json.Unmarshal(data, &warning); err != nil {
			return nil, nil, fmt.Errorf("decode warning: %w", err)
		}
		return LiveWarningEvent{Warning: warning}, nil, nil
	case "error":
		var message liveproto.ServerError
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, nil, fmt.Errorf("decode error: %w", err)
		}
		return LiveErrorEvent{Error: message}, nil, nil
	case "audio_in_ack":
		var ack liveproto.ServerAudioInAck
		if err := json.Unmarshal(data, &ack); err != nil {
			return nil, nil, fmt.Errorf("decode audio_in_ack: %w", err)
		}
		return LiveAudioInAckEvent{Ack: ack}, nil, nil
	case "transcript_delta":
		var delta liveproto.ServerTranscriptDelta
		if err := json.Unmarshal(data, &delta); err != nil {
			return nil, nil, fmt.Errorf("decode transcript_delta: %w", err)
		}
		return LiveTranscriptDeltaEvent{
			UtteranceID: delta.UtteranceID,
			IsFinal:     delta.IsFinal,
			Text:        delta.Text,
			Stability:   delta.Stability,
			TimestampMS: delta.TimestampMS,
		}, nil, nil
	case "utterance_final":
		var final liveproto.ServerUtteranceFinal
		if err := json.Unmarshal(data, &final); err != nil {
			return nil, nil, fmt.Errorf("decode utterance_final: %w", err)
		}
		return LiveUtteranceFinalEvent{
			UtteranceID: final.UtteranceID,
			Text:        final.Text,
			EndMS:       final.EndMS,
		}, nil, nil
	case "assistant_audio_start":
		var start liveproto.ServerAssistantAudioStart
		if err := json.Unmarshal(data, &start); err != nil {
			return nil, nil, fmt.Errorf("decode assistant_audio_start: %w", err)
		}
		assistantFormat[strings.TrimSpace(start.AssistantAudioID)] = strings.TrimSpace(start.Format.Encoding)
		return LiveAssistantAudioStartEvent{Start: start}, nil, nil
	case "assistant_audio_chunk":
		var chunk liveproto.ServerAssistantAudioChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil, nil, fmt.Errorf("decode assistant_audio_chunk: %w", err)
		}
		audioBytes, err := base64.StdEncoding.DecodeString(chunk.AudioB64)
		if err != nil {
			return nil, nil, fmt.Errorf("decode assistant audio chunk: %w", err)
		}
		format := strings.TrimSpace(assistantFormat[strings.TrimSpace(chunk.AssistantAudioID)])
		if format == "" {
			format = "pcm_s16le"
		}
		return LiveAssistantAudioChunkEvent{
			AssistantAudioID: chunk.AssistantAudioID,
			Seq:              chunk.Seq,
			Data:             audioBytes,
			Format:           format,
			Alignment:        chunk.Alignment,
		}, nil, nil
	case "assistant_audio_chunk_header":
		var header liveproto.ServerAssistantAudioChunkHeader
		if err := json.Unmarshal(data, &header); err != nil {
			return nil, nil, fmt.Errorf("decode assistant_audio_chunk_header: %w", err)
		}
		*pendingBinaryHeader = &header
		return nil, nil, nil
	case "assistant_audio_end":
		var end liveproto.ServerAssistantAudioEnd
		if err := json.Unmarshal(data, &end); err != nil {
			return nil, nil, fmt.Errorf("decode assistant_audio_end: %w", err)
		}
		delete(assistantFormat, strings.TrimSpace(end.AssistantAudioID))
		return LiveAssistantAudioEndEvent{End: end}, nil, nil
	case "assistant_text_delta":
		var delta liveproto.ServerAssistantTextDelta
		if err := json.Unmarshal(data, &delta); err != nil {
			return nil, nil, fmt.Errorf("decode assistant_text_delta: %w", err)
		}
		return LiveAssistantTextDeltaEvent{
			AssistantAudioID: delta.AssistantAudioID,
			Delta:            delta.Delta,
		}, nil, nil
	case "assistant_text_final":
		var final liveproto.ServerAssistantTextFinal
		if err := json.Unmarshal(data, &final); err != nil {
			return nil, nil, fmt.Errorf("decode assistant_text_final: %w", err)
		}
		return LiveAssistantTextFinalEvent{
			AssistantAudioID: final.AssistantAudioID,
			Text:             final.Text,
		}, nil, nil
	case "audio_reset":
		var reset liveproto.ServerAudioReset
		if err := json.Unmarshal(data, &reset); err != nil {
			return nil, nil, fmt.Errorf("decode audio_reset: %w", err)
		}
		return LiveAudioResetEvent{
			Reason:           reset.Reason,
			AssistantAudioID: reset.AssistantAudioID,
		}, nil, nil
	case "run_event":
		var frame liveproto.ServerRunEvent
		if err := json.Unmarshal(data, &frame); err != nil {
			return nil, nil, fmt.Errorf("decode run_event: %w", err)
		}
		ev, err := types.UnmarshalRunStreamEvent(frame.Event)
		if err != nil {
			return nil, nil, fmt.Errorf("decode run_event payload: %w", err)
		}
		return LiveRunEvent{TurnID: frame.TurnID, Event: ev}, ev, nil
	case "tool_call":
		var call liveproto.ServerToolCall
		if err := json.Unmarshal(data, &call); err != nil {
			return nil, nil, fmt.Errorf("decode tool_call: %w", err)
		}
		return LiveToolCallEvent{
			TurnID: call.TurnID,
			ID:     call.ID,
			Name:   call.Name,
			Input:  call.Input,
		}, nil, nil
	case "tool_cancel":
		var cancel liveproto.ServerToolCancel
		if err := json.Unmarshal(data, &cancel); err != nil {
			return nil, nil, fmt.Errorf("decode tool_cancel: %w", err)
		}
		return LiveToolCancelEvent{
			TurnID: cancel.TurnID,
			ID:     cancel.ID,
			Reason: cancel.Reason,
		}, nil, nil
	default:
		return LiveUnknownEvent{
			Type: typ,
			Raw:  append(json.RawMessage(nil), data...),
		}, nil, nil
	}
}

// Connect opens a low-level /v1/live websocket session.
func (s *LiveService) Connect(ctx context.Context, req *LiveConnectRequest) (*LiveSession, error) {
	return s.connect(ctx, req)
}

func (s *LiveService) connect(ctx context.Context, req *LiveConnectRequest) (*LiveSession, error) {
	if s == nil || s.client == nil {
		return nil, core.NewInvalidRequestError("live service is not initialized")
	}
	if !s.client.isProxyMode() {
		return nil, core.NewInvalidRequestError("Live.Connect requires proxy mode (set WithBaseURL)")
	}
	if req == nil {
		return nil, core.NewInvalidRequestError("req must not be nil")
	}

	wsURL, err := s.client.gatewayWebSocketEndpoint("/v1/live")
	if err != nil {
		return nil, err
	}

	normalized, err := normalizeLiveConnectRequest(*req)
	if err != nil {
		return nil, err
	}

	hello, err := s.client.buildLiveHello(normalized)
	if err != nil {
		return nil, err
	}

	headers := make(http.Header)
	headers.Set(vaiVersionHeader, vaiVersionValue)
	if s.client.gatewayAPIKey != "" {
		headers.Set("Authorization", "Bearer "+s.client.gatewayAPIKey)
	}

	dialer := websocket.DefaultDialer
	if dialer == nil {
		dialer = &websocket.Dialer{}
	}

	dialCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		dialCtx, cancel = context.WithTimeout(ctx, defaultLiveConnectTimeout)
		defer cancel()
	}

	conn, resp, err := dialer.DialContext(dialCtx, wsURL, headers)
	if err != nil {
		if resp != nil {
			return nil, &TransportError{Op: "GET", URL: wsURL, Err: fmt.Errorf("websocket dial failed (status %d): %w", resp.StatusCode, err)}
		}
		return nil, &TransportError{Op: "GET", URL: wsURL, Err: err}
	}

	if err := conn.WriteJSON(hello); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send live hello: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(defaultLiveConnectTimeout))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read hello_ack: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if messageType != websocket.TextMessage {
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected first live frame type %d", messageType)
	}

	firstEvent, _, err := decodeLiveTextFrame(payload, map[string]string{}, new(*liveproto.ServerAssistantAudioChunkHeader))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	switch e := firstEvent.(type) {
	case LiveHelloAckEvent:
		session := &LiveSession{
			conn:      conn,
			events:    make(chan LiveEvent, 256),
			runEvents: make(chan types.RunStreamEvent, 256),
			done:      make(chan struct{}),
		}
		// Surface hello_ack to consumers too.
		session.emitEvent(e)
		go session.readLoop()
		return session, nil
	case LiveErrorEvent:
		_ = conn.Close()
		return nil, &core.Error{
			Type:    core.ErrAPI,
			Message: strings.TrimSpace(e.Error.Message),
			Code:    strings.TrimSpace(e.Error.Code),
		}
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected first live frame type %q", firstEvent.liveEventType())
	}
}

func normalizeLiveConnectRequest(req LiveConnectRequest) (LiveConnectRequest, error) {
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		return LiveConnectRequest{}, core.NewInvalidRequestError("live model must not be empty")
	}
	req.System = strings.TrimSpace(req.System)

	req.Voice.Provider = strings.ToLower(strings.TrimSpace(req.Voice.Provider))
	if req.Voice.Provider == "" {
		req.Voice.Provider = liveproto.VoiceProviderCartesia
	}
	if req.Voice.Provider != liveproto.VoiceProviderCartesia && req.Voice.Provider != liveproto.VoiceProviderElevenLabs {
		return LiveConnectRequest{}, core.NewInvalidRequestError("live voice provider must be cartesia or elevenlabs")
	}
	req.Voice.VoiceID = strings.TrimSpace(req.Voice.VoiceID)
	if req.Voice.VoiceID == "" {
		return LiveConnectRequest{}, core.NewInvalidRequestError("live voice_id is required")
	}
	req.Voice.Language = strings.TrimSpace(req.Voice.Language)
	if req.Voice.Language == "" {
		req.Voice.Language = "en"
	}

	req.Features.AudioTransport = strings.TrimSpace(req.Features.AudioTransport)
	if req.Features.AudioTransport == "" {
		req.Features.AudioTransport = liveproto.AudioTransportBase64JSON
	}

	return req, nil
}

func (c *Client) buildLiveHello(req LiveConnectRequest) (liveproto.ClientHello, error) {
	provider, _, err := core.ParseModelString(req.Model)
	if err != nil {
		return liveproto.ClientHello{}, err
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	voiceProvider := strings.ToLower(strings.TrimSpace(req.Voice.Provider))
	if voiceProvider == "" {
		voiceProvider = liveproto.VoiceProviderCartesia
	}

	byok, err := c.buildLiveBYOK(provider, voiceProvider, req.BYOK)
	if err != nil {
		return liveproto.ClientHello{}, err
	}

	hello := liveproto.ClientHello{
		Type:            "hello",
		ProtocolVersion: liveproto.ProtocolVersion1,
		Model:           req.Model,
		System:          req.System,
		Messages:        req.Messages,
		BYOK:            byok,
		AudioIn: liveproto.AudioFormat{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
		AudioOut: liveproto.AudioFormat{
			Encoding:     "pcm_s16le",
			SampleRateHz: 24000,
			Channels:     1,
		},
		Voice: &liveproto.HelloVoice{
			Provider: voiceProvider,
			VoiceID:  req.Voice.VoiceID,
			Language: req.Voice.Language,
			Speed:    req.Voice.Speed,
			Volume:   req.Voice.Volume,
			Emotion:  req.Voice.Emotion,
		},
		Features: liveproto.HelloFeatures{
			AudioTransport:         req.Features.AudioTransport,
			SendPlaybackMarks:      req.Features.SendPlaybackMarks,
			WantPartialTranscripts: req.Features.WantPartialTranscripts,
			WantAssistantText:      req.Features.WantAssistantText,
			ClientHasAEC:           req.Features.ClientHasAEC,
			WantRunEvents:          req.Features.WantRunEvents,
		},
	}
	if c.gatewayAPIKey != "" {
		hello.Auth = &liveproto.HelloAuth{
			Mode:          "api_key",
			GatewayAPIKey: c.gatewayAPIKey,
		}
	}
	if len(req.Tools.ServerTools) > 0 || len(req.Tools.ServerToolConfig) > 0 || len(req.Tools.ClientTools) > 0 {
		hello.Tools = &liveproto.HelloTools{
			ServerTools:      req.Tools.ServerTools,
			ServerToolConfig: req.Tools.ServerToolConfig,
			ClientTools:      req.Tools.ClientTools,
		}
	}
	return hello, nil
}

func (c *Client) buildLiveBYOK(modelProvider, voiceProvider string, overrides map[string]string) (liveproto.HelloBYOK, error) {
	override := make(map[string]string, len(overrides))
	for provider, key := range overrides {
		provider = strings.ToLower(strings.TrimSpace(provider))
		key = strings.TrimSpace(key)
		if provider == "" || key == "" {
			continue
		}
		override[provider] = key
	}

	lookup := func(provider string) string {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if key := strings.TrimSpace(override[provider]); key != "" {
			return key
		}
		if provider == "cartesia" {
			if key := strings.TrimSpace(c.getCartesiaAPIKey()); key != "" {
				return key
			}
		}
		if provider == "elevenlabs" {
			if key := strings.TrimSpace(c.providerKeyForProvider("elevenlabs")); key != "" {
				return key
			}
			return strings.TrimSpace(os.Getenv("ELEVENLABS_API_KEY"))
		}
		return strings.TrimSpace(c.providerKeyForProvider(provider))
	}

	modelProvider = strings.ToLower(strings.TrimSpace(modelProvider))
	modelKey := lookup(modelProvider)
	if modelKey == "" {
		return liveproto.HelloBYOK{}, fmt.Errorf("missing provider key for %s (set %s)", modelProvider, liveProviderEnvHint(modelProvider))
	}

	cartesiaKey := lookup("cartesia")
	if cartesiaKey == "" {
		return liveproto.HelloBYOK{}, fmt.Errorf("missing provider key for cartesia (set CARTESIA_API_KEY)")
	}

	byok := liveproto.HelloBYOK{
		Keys: map[string]string{},
	}
	set := func(provider, key string) {
		provider = strings.ToLower(strings.TrimSpace(provider))
		key = strings.TrimSpace(key)
		if provider == "" || key == "" {
			return
		}
		switch provider {
		case "anthropic":
			byok.Anthropic = key
		case "openai", "oai-resp":
			byok.OpenAI = key
		case "gemini", "gemini-oauth":
			byok.Gemini = key
		case "groq":
			byok.Groq = key
		case "cerebras":
			byok.Cerebras = key
		case "openrouter":
			byok.OpenRouter = key
		case "cartesia":
			byok.Cartesia = key
		case "elevenlabs":
			byok.ElevenLabs = key
		}
		byok.Keys[provider] = key
	}

	set(modelProvider, modelKey)
	set("cartesia", cartesiaKey)

	voiceProvider = strings.ToLower(strings.TrimSpace(voiceProvider))
	if voiceProvider == liveproto.VoiceProviderElevenLabs {
		elevenKey := lookup("elevenlabs")
		if elevenKey == "" {
			return liveproto.HelloBYOK{}, fmt.Errorf("missing provider key for elevenlabs (set ELEVENLABS_API_KEY)")
		}
		set("elevenlabs", elevenKey)
	}

	// Add web tool provider keys when available so gateway can infer providers.
	for _, provider := range []string{"tavily", "exa", "firecrawl"} {
		set(provider, lookup(provider))
	}

	// Carry explicit overrides for unknown providers too.
	for provider, key := range override {
		if _, exists := byok.Keys[provider]; exists {
			continue
		}
		byok.Keys[provider] = key
	}
	return byok, nil
}

func liveProviderEnvHint(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "oai-resp", "openai":
		return "OPENAI_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "gemini", "gemini-oauth":
		return "GEMINI_API_KEY (or GOOGLE_API_KEY)"
	case "groq":
		return "GROQ_API_KEY"
	case "cerebras":
		return "CEREBRAS_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "cartesia":
		return "CARTESIA_API_KEY"
	case "elevenlabs":
		return "ELEVENLABS_API_KEY"
	default:
		return strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"
	}
}

func (c *Client) gatewayWebSocketEndpoint(path string) (string, error) {
	endpoint, err := c.gatewayEndpoint(path)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", core.NewInvalidRequestError("invalid gateway base URL")
	}
	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already websocket scheme.
	default:
		return "", core.NewInvalidRequestError("gateway base URL must use http(s) or ws(s)")
	}
	return u.String(), nil
}

// LiveRunStream provides a RunStream-like event stream over a live websocket session.
type LiveRunStream struct {
	session *LiveSession
	ctx     context.Context

	events chan RunStreamEvent
	done   chan struct{}

	mu        sync.RWMutex
	result    *RunResult
	err       error
	closeOnce sync.Once

	toolHandlers map[string]ToolHandler
	onToolCall   func(name string, input map[string]any, output any, err error)
	toolTimeout  time.Duration

	activeToolMu      sync.Mutex
	activeToolCancels map[string]context.CancelFunc
}

// RunStream opens a live session and returns a RunStream-like event stream.
func (s *LiveService) RunStream(ctx context.Context, req *LiveRunRequest, opts ...RunOption) (*LiveRunStream, error) {
	if req == nil {
		return nil, core.NewInvalidRequestError("req must not be nil")
	}
	cfg := defaultRunConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.toolTimeout <= 0 {
		cfg.toolTimeout = 30 * time.Second
	}

	clientTools, err := buildLiveClientTools(req.Tools, cfg)
	if err != nil {
		return nil, err
	}

	connectReq := &LiveConnectRequest{
		Model:    req.Model,
		System:   req.System,
		Messages: req.Messages,
		Voice:    req.Voice,
		Features: req.Features,
		Tools: LiveTools{
			ServerTools:      req.ServerTools,
			ServerToolConfig: req.ServerToolConfig,
			ClientTools:      clientTools,
		},
	}
	// RunStream-like behavior defaults to run events + captions.
	if strings.TrimSpace(connectReq.Features.AudioTransport) == "" {
		connectReq.Features.AudioTransport = liveproto.AudioTransportBase64JSON
	}
	connectReq.Features.WantRunEvents = true
	connectReq.Features.WantAssistantText = true
	connectReq.Features.WantPartialTranscripts = true
	connectReq.Features.SendPlaybackMarks = true
	connectReq.Features.ClientHasAEC = true

	session, err := s.connect(ctx, connectReq)
	if err != nil {
		return nil, err
	}

	stream := &LiveRunStream{
		session:           session,
		ctx:               ctx,
		events:            make(chan RunStreamEvent, 512),
		done:              make(chan struct{}),
		toolHandlers:      cfg.toolHandlers,
		onToolCall:        cfg.onToolCall,
		toolTimeout:       cfg.toolTimeout,
		activeToolCancels: make(map[string]context.CancelFunc),
	}
	go stream.readLoop()
	return stream, nil
}

func buildLiveClientTools(reqTools []types.Tool, cfg runConfig) ([]types.Tool, error) {
	merged := mergeTools(reqTools, cfg.extraTools)
	clientTools := make([]types.Tool, 0, len(merged))
	defs := make(map[string]struct{}, len(merged))
	for _, tool := range merged {
		if strings.TrimSpace(tool.Type) != types.ToolTypeFunction {
			return nil, fmt.Errorf("live client tools must be function tools (got type=%q)", strings.TrimSpace(tool.Type))
		}
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return nil, fmt.Errorf("live client function tool name must not be empty")
		}
		key := strings.ToLower(name)
		switch key {
		case "talk_to_user", "vai_web_search", "vai_web_fetch":
			return nil, fmt.Errorf("live client function tool name %q is reserved", name)
		}
		defs[key] = struct{}{}
		clientTools = append(clientTools, tool)
	}

	for name := range cfg.toolHandlers {
		if _, ok := defs[strings.ToLower(strings.TrimSpace(name))]; ok {
			continue
		}
		return nil, fmt.Errorf("tool handler registered for %q but no function tool definition was provided", name)
	}
	return clientTools, nil
}

func (s *LiveRunStream) readLoop() {
	defer close(s.done)
	defer close(s.events)

	for event := range s.session.Events() {
		switch e := event.(type) {
		case LiveAssistantTextDeltaEvent:
			s.sendEvent(StreamEventWrapper{
				Event: types.ContentBlockDeltaEvent{
					Type:  "content_block_delta",
					Index: 0,
					Delta: types.TextDelta{Type: "text_delta", Text: e.Delta},
				},
			})
		case LiveAssistantAudioChunkEvent:
			s.sendEvent(AudioChunkEvent{Data: e.Data, Format: e.Format})
		case LiveTranscriptDeltaEvent:
			s.sendEvent(e)
		case LiveUtteranceFinalEvent:
			s.sendEvent(e)
		case LiveAudioResetEvent:
			s.sendEvent(e)
		case LiveRunEvent:
			s.handleRunEvent(e.Event)
		case LiveToolCallEvent:
			s.handleToolCall(e)
		case LiveToolCancelEvent:
			s.handleToolCancel(e)
		case LiveErrorEvent:
			s.setErr(core.NewAPIError(strings.TrimSpace(e.Error.Message)))
		}
	}

	if err := s.session.Err(); err != nil {
		s.setErr(err)
	}
}

func (s *LiveRunStream) handleRunEvent(event types.RunStreamEvent) {
	if event == nil {
		return
	}
	switch e := event.(type) {
	case types.RunStepStartEvent:
		s.sendEvent(StepStartEvent{Index: e.Index})
	case types.RunStreamEventWrapper:
		s.sendEvent(StreamEventWrapper{Event: e.Event})
	case types.RunToolCallStartEvent:
		s.sendEvent(ToolCallStartEvent{ID: e.ID, Name: e.Name, Input: e.Input})
	case types.RunToolResultEvent:
		var toolErr error
		if e.Error != nil {
			toolErr = typesErrorToCoreError(*e.Error)
		}
		s.sendEvent(ToolResultEvent{ID: e.ID, Name: e.Name, Content: e.Content, Error: toolErr})
	case types.RunStepCompleteEvent:
		var resp *Response
		if e.Response != nil {
			resp = &Response{MessageResponse: e.Response}
		}
		s.sendEvent(StepCompleteEvent{Index: e.Index, Response: resp})
	case types.RunHistoryDeltaEvent:
		s.sendEvent(HistoryDeltaEvent{ExpectedLen: e.ExpectedLen, Append: e.Append})
	case types.RunCompleteEvent:
		result := runResultFromTypes(e.Result)
		s.mu.Lock()
		s.result = result
		s.mu.Unlock()
		s.sendEvent(RunCompleteEvent{Result: result})
	case types.RunErrorEvent:
		s.setErr(typesErrorToCoreError(e.Error))
	}
}

func runResultFromTypes(result *types.RunResult) *RunResult {
	if result == nil {
		return nil
	}
	out := &RunResult{
		ToolCallCount: result.ToolCallCount,
		TurnCount:     result.TurnCount,
		Usage:         result.Usage,
		StopReason:    RunStopReason(result.StopReason),
		Messages:      append([]types.Message(nil), result.Messages...),
		Steps:         make([]RunStep, 0, len(result.Steps)),
	}
	if result.Response != nil {
		out.Response = &Response{MessageResponse: result.Response}
	}
	for _, step := range result.Steps {
		converted := RunStep{
			Index:      step.Index,
			ToolCalls:  make([]ToolCall, 0, len(step.ToolCalls)),
			DurationMs: step.DurationMS,
		}
		if step.Response != nil {
			converted.Response = &Response{MessageResponse: step.Response}
		}
		for _, call := range step.ToolCalls {
			converted.ToolCalls = append(converted.ToolCalls, ToolCall{
				ID:    call.ID,
				Name:  call.Name,
				Input: call.Input,
			})
		}
		if len(step.ToolResults) > 0 {
			converted.ToolResults = make([]ToolExecutionResult, 0, len(step.ToolResults))
			for _, toolResult := range step.ToolResults {
				entry := ToolExecutionResult{
					ToolUseID: toolResult.ToolUseID,
					Content:   toolResult.Content,
				}
				if toolResult.Error != nil {
					err := typesErrorToCoreError(*toolResult.Error)
					entry.Error = err
					entry.ErrorMsg = err.Error()
				}
				converted.ToolResults = append(converted.ToolResults, entry)
			}
		}
		out.Steps = append(out.Steps, converted)
	}
	return out
}

func liveToolKey(turnID int, id string) string {
	return fmt.Sprintf("%d:%s", turnID, strings.TrimSpace(id))
}

func (s *LiveRunStream) handleToolCall(call LiveToolCallEvent) {
	handler, ok := s.toolHandlers[strings.TrimSpace(call.Name)]
	if !ok {
		_ = s.session.SendToolResult(LiveToolResult{
			TurnID: call.TurnID,
			ID:     call.ID,
			Content: []types.ContentBlock{
				types.TextBlock{
					Type: "text",
					Text: fmt.Sprintf("Tool '%s' was called but no handler is registered.", strings.TrimSpace(call.Name)),
				},
			},
			IsError: true,
			Error: &types.Error{
				Type:    string(core.ErrInvalidRequest),
				Message: "tool handler not registered",
				Code:    "tool_not_registered",
			},
		})
		return
	}

	toolCtx := s.ctx
	cancel := func() {}
	if s.toolTimeout > 0 {
		var c context.CancelFunc
		toolCtx, c = context.WithTimeout(s.ctx, s.toolTimeout)
		cancel = c
	}
	key := liveToolKey(call.TurnID, call.ID)
	s.activeToolMu.Lock()
	s.activeToolCancels[key] = cancel
	s.activeToolMu.Unlock()

	go func() {
		defer func() {
			cancel()
			s.activeToolMu.Lock()
			delete(s.activeToolCancels, key)
			s.activeToolMu.Unlock()
		}()

		inputJSON, err := json.Marshal(call.Input)
		if err != nil {
			_ = s.session.SendToolResult(LiveToolResult{
				TurnID: call.TurnID,
				ID:     call.ID,
				Content: []types.ContentBlock{
					types.TextBlock{Type: "text", Text: fmt.Sprintf("Error marshaling tool input: %v", err)},
				},
				IsError: true,
				Error: &types.Error{
					Type:    string(core.ErrInvalidRequest),
					Message: "invalid tool input payload",
					Code:    "tool_input_invalid",
				},
			})
			if s.onToolCall != nil {
				s.onToolCall(call.Name, call.Input, nil, err)
			}
			return
		}

		output, execErr := handler(toolCtx, inputJSON)
		if s.onToolCall != nil {
			s.onToolCall(call.Name, call.Input, output, execErr)
		}

		if errors.Is(execErr, context.Canceled) {
			// Turn was interrupted/cancelled; gateway is no longer waiting on this tool call.
			return
		}
		if errors.Is(execErr, context.DeadlineExceeded) {
			_ = s.session.SendToolResult(LiveToolResult{
				TurnID: call.TurnID,
				ID:     call.ID,
				Content: []types.ContentBlock{
					types.TextBlock{Type: "text", Text: "Tool execution timed out."},
				},
				IsError: true,
				Error: &types.Error{
					Type:    string(core.ErrAPI),
					Message: "tool execution timed out",
					Code:    "tool_timeout",
				},
			})
			return
		}
		if execErr != nil {
			_ = s.session.SendToolResult(LiveToolResult{
				TurnID: call.TurnID,
				ID:     call.ID,
				Content: []types.ContentBlock{
					types.TextBlock{Type: "text", Text: fmt.Sprintf("Error executing tool: %v", execErr)},
				},
				IsError: true,
				Error: &types.Error{
					Type:    string(core.ErrAPI),
					Message: strings.TrimSpace(execErr.Error()),
					Code:    "tool_execution_failed",
				},
			})
			return
		}

		_ = s.session.SendToolResult(LiveToolResult{
			TurnID:  call.TurnID,
			ID:      call.ID,
			Content: outputToContentBlocks(output),
		})
	}()
}

func (s *LiveRunStream) handleToolCancel(cancel LiveToolCancelEvent) {
	key := liveToolKey(cancel.TurnID, cancel.ID)
	s.activeToolMu.Lock()
	fn := s.activeToolCancels[key]
	s.activeToolMu.Unlock()
	if fn != nil {
		fn()
	}
}

func (s *LiveRunStream) sendEvent(event RunStreamEvent) {
	if event == nil {
		return
	}
	select {
	case s.events <- event:
	default:
		// Avoid deadlocks on slow consumers; live sessions can be long-running.
	}
}

func (s *LiveRunStream) setErr(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

// Events yields RunStream-like events from the live session.
func (s *LiveRunStream) Events() <-chan RunStreamEvent {
	if s == nil {
		return nil
	}
	return s.events
}

// Result returns the latest run_complete result observed during the session.
func (s *LiveRunStream) Result() *RunResult {
	if s == nil {
		return nil
	}
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result
}

// Err returns the terminal stream/session error.
func (s *LiveRunStream) Err() error {
	if s == nil {
		return nil
	}
	<-s.done
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

// Close closes the underlying live session.
func (s *LiveRunStream) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.closeOnce.Do(func() {
		closeErr = s.session.Close()
	})
	<-s.done
	return closeErr
}

// SendAudioFrame forwards an audio frame to the underlying live session.
func (s *LiveRunStream) SendAudioFrame(pcm []byte, meta LiveAudioMeta) error {
	if s == nil || s.session == nil {
		return fmt.Errorf("live stream is not initialized")
	}
	return s.session.SendAudioFrame(pcm, meta)
}

// SendPlaybackMark forwards a playback mark to the underlying live session.
func (s *LiveRunStream) SendPlaybackMark(mark LivePlaybackMark) error {
	if s == nil || s.session == nil {
		return fmt.Errorf("live stream is not initialized")
	}
	return s.session.SendPlaybackMark(mark)
}

// Interrupt forwards an interrupt control frame to the live session.
func (s *LiveRunStream) Interrupt() error {
	if s == nil || s.session == nil {
		return fmt.Errorf("live stream is not initialized")
	}
	return s.session.Interrupt()
}

// CancelTurn forwards a cancel_turn control frame to the live session.
func (s *LiveRunStream) CancelTurn() error {
	if s == nil || s.session == nil {
		return fmt.Errorf("live stream is not initialized")
	}
	return s.session.CancelTurn()
}

// EndSession forwards an end_session control frame to the live session.
func (s *LiveRunStream) EndSession() error {
	if s == nil || s.session == nil {
		return fmt.Errorf("live stream is not initialized")
	}
	return s.session.EndSession()
}
