package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/types"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
	"github.com/vango-go/vai-lite/pkg/gateway/live/protocol"
	"github.com/vango-go/vai-lite/pkg/gateway/runloop"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
)

const (
	talkToUserToolName = "talk_to_user"
	voiceSystemPrompt  = "You are a real-time voice assistant. Use tools when needed, and call talk_to_user({text}) for final speech output. Do not emit markdown. Expand numbers, symbols, and abbreviations for speech."

	maxCanceledAssistantAudioIDs = 64
	outboundPriorityQueueSize    = 8
)

var errBackpressure = errors.New("live outbound backpressure")
var errAudioWindowBackpressure = errors.New("live unplayed audio window exceeded")

type STTConfig struct {
	Model      string
	Language   string
	Encoding   string
	SampleRate int
}

type STTSession interface {
	SendAudio([]byte) error
	FinalizeUtterance() error
	Deltas() <-chan stt.TranscriptDelta
	Close() error
}

type STTProvider interface {
	NewSession(ctx context.Context, cfg STTConfig) (STTSession, error)
}

type TTSConfig struct {
	Voice            string
	Language         string
	Speed            float64
	Volume           float64
	Emotion          string
	Format           string
	SampleRate       int
	MaxBufferDelayMS int
}

type TTSContext interface {
	SendText(text string, isFinal bool) error
	Flush() error
	Audio() <-chan []byte
	Done() <-chan struct{}
	Err() error
	Close() error
}

type TTSProvider interface {
	NewContext(ctx context.Context, cfg TTSConfig) (TTSContext, error)
}

type STTProviderAdapter struct {
	Provider stt.Provider
}

func (a STTProviderAdapter) NewSession(ctx context.Context, cfg STTConfig) (STTSession, error) {
	if a.Provider == nil {
		return nil, fmt.Errorf("stt provider is nil")
	}
	s, err := a.Provider.NewStreamingSTT(ctx, stt.TranscribeOptions{
		Model:      cfg.Model,
		Language:   cfg.Language,
		Format:     cfg.Encoding,
		SampleRate: cfg.SampleRate,
	})
	if err != nil {
		return nil, err
	}
	return sttSessionAdapter{inner: s}, nil
}

type sttSessionAdapter struct {
	inner *stt.StreamingSTT
}

func (a sttSessionAdapter) SendAudio(data []byte) error {
	if a.inner == nil {
		return fmt.Errorf("stt session is nil")
	}
	return a.inner.SendAudio(data)
}

func (a sttSessionAdapter) FinalizeUtterance() error {
	if a.inner == nil {
		return fmt.Errorf("stt session is nil")
	}
	return a.inner.Finalize()
}

func (a sttSessionAdapter) Deltas() <-chan stt.TranscriptDelta {
	if a.inner == nil {
		ch := make(chan stt.TranscriptDelta)
		close(ch)
		return ch
	}
	return a.inner.Transcripts()
}

func (a sttSessionAdapter) Close() error {
	if a.inner == nil {
		return nil
	}
	return a.inner.Close()
}

type TTSProviderAdapter struct {
	Provider tts.Provider
}

func (a TTSProviderAdapter) NewContext(ctx context.Context, cfg TTSConfig) (TTSContext, error) {
	if a.Provider == nil {
		return nil, fmt.Errorf("tts provider is nil")
	}
	return a.Provider.NewStreamingContext(ctx, tts.StreamingContextOptions{
		Voice:            cfg.Voice,
		Language:         cfg.Language,
		Speed:            cfg.Speed,
		Volume:           cfg.Volume,
		Emotion:          cfg.Emotion,
		Format:           cfg.Format,
		SampleRate:       cfg.SampleRate,
		MaxBufferDelayMs: cfg.MaxBufferDelayMS,
	})
}

type Config struct {
	MaxAudioFrameBytes         int
	MaxJSONMessageBytes        int64
	LiveMaxAudioFPS            int
	LiveMaxAudioBytesPerSecond int64
	LiveInboundBurstSeconds    int
	SilenceCommit              time.Duration
	GracePeriod                time.Duration
	PingInterval               time.Duration
	WriteTimeout               time.Duration
	ReadTimeout                time.Duration
	MaxSessionDuration         time.Duration
	TurnTimeout                time.Duration
	ToolTimeout                time.Duration
	MaxToolCallsPerTurn        int
	MaxModelCallsPerTurn       int
	MaxUnplayedDuration        time.Duration
	PlaybackStopWait           time.Duration
	MaxBackpressurePerMin      int
	ElevenLabsWSBaseURL        string
	OutboundQueueSize          int
	AudioInAckEveryN           int
	AudioTransportBinary       bool
}

type Dependencies struct {
	Conn        *websocket.Conn
	Logger      *slog.Logger
	Provider    core.Provider
	STT         STTProvider
	TTS         TTSProvider
	ServerTools *servertools.Registry
	Hello       protocol.ClientHello
	SessionID   string
	RequestID   string
	ModelName   string
	Config      Config
	StartTime   time.Time
	Now         func() time.Time
}

type LiveSession struct {
	conn        *websocket.Conn
	logger      *slog.Logger
	provider    core.Provider
	stt         STTProvider
	tts         TTSProvider
	serverTools *servertools.Registry
	hello       protocol.ClientHello
	sessionID   string
	requestID   string
	modelName   string
	cfg         Config
	startTime   time.Time
	now         func() time.Time

	ctx    context.Context
	cancel context.CancelFunc

	outboundPriority chan outboundFrame
	outboundNormal   chan outboundFrame

	canceledAssistant atomic.Value // canceledAssistantState
	assistantCounter  atomic.Int64

	clockHaveClient          atomic.Bool
	clockMaxClientMS         atomic.Int64
	clockMaxClientAtUnixNano atomic.Int64

	clientTools     map[string]types.Tool
	toolWaitersMu   sync.Mutex
	toolResultQueue map[string]chan protocol.ClientToolResult
}

type outboundFrame struct {
	isAssistantAudio bool
	assistantAudioID string

	textPayload   []byte
	binaryPayload []byte
	binaryPair    *binaryPair
}

type binaryPair struct {
	header []byte
	data   []byte
}

type canceledAssistantState struct {
	set   map[string]struct{}
	order []string
}

type inboundFrame struct {
	messageType int
	data        []byte
	err         error
}

type runResult struct {
	turnID      int
	text        string
	assistantID string
	segment     *speechSegment
	err         error
}

type ttsResult struct {
	turnID      int
	assistantID string
	text        string
	completed   bool
	canceled    bool
	err         error
}

type assistantStart struct {
	turnID      int
	assistantID string
	segment     *speechSegment
	ttsCancel   context.CancelFunc
}

type runTurnDeps struct {
	assistantStartCh chan<- assistantStart
	ttsDoneCh        chan<- ttsResult
	voiceProvider    string
	elevenConn       *elevenLabsLiveConn
}

type runTurnResult struct {
	text        string
	assistantID string
	segment     *speechSegment
}

func New(deps Dependencies) (*LiveSession, error) {
	if deps.Conn == nil {
		return nil, fmt.Errorf("connection is required")
	}
	if deps.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	if deps.STT == nil {
		return nil, fmt.Errorf("stt provider is required")
	}
	voiceProvider := protocol.VoiceProviderCartesia
	if deps.Hello.Voice != nil && strings.TrimSpace(deps.Hello.Voice.Provider) != "" {
		voiceProvider = strings.ToLower(strings.TrimSpace(deps.Hello.Voice.Provider))
	}
	if deps.TTS == nil && voiceProvider != protocol.VoiceProviderElevenLabs {
		return nil, fmt.Errorf("tts provider is required")
	}
	if strings.TrimSpace(deps.ModelName) == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Config.OutboundQueueSize <= 0 {
		deps.Config.OutboundQueueSize = 128
	}
	if deps.Config.AudioInAckEveryN <= 0 {
		deps.Config.AudioInAckEveryN = 25
	}
	if deps.Config.ToolTimeout <= 0 {
		deps.Config.ToolTimeout = 10 * time.Second
	}
	if deps.Config.MaxToolCallsPerTurn <= 0 {
		deps.Config.MaxToolCallsPerTurn = 5
	}
	if deps.Config.MaxModelCallsPerTurn <= 0 {
		deps.Config.MaxModelCallsPerTurn = 8
	}
	if deps.Config.MaxUnplayedDuration <= 0 {
		deps.Config.MaxUnplayedDuration = 2500 * time.Millisecond
	}
	if deps.Config.PlaybackStopWait <= 0 {
		deps.Config.PlaybackStopWait = 500 * time.Millisecond
	}
	if deps.Config.MaxBackpressurePerMin <= 0 {
		deps.Config.MaxBackpressurePerMin = 3
	}
	if deps.StartTime.IsZero() {
		deps.StartTime = time.Now()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &LiveSession{
		conn:             deps.Conn,
		logger:           deps.Logger,
		provider:         deps.Provider,
		stt:              deps.STT,
		tts:              deps.TTS,
		serverTools:      deps.ServerTools,
		hello:            deps.Hello,
		sessionID:        deps.SessionID,
		requestID:        deps.RequestID,
		modelName:        deps.ModelName,
		cfg:              deps.Config,
		startTime:        deps.StartTime,
		now:              deps.Now,
		ctx:              ctx,
		cancel:           cancel,
		outboundPriority: make(chan outboundFrame, max(1, min(deps.Config.OutboundQueueSize, outboundPriorityQueueSize))),
		outboundNormal:   make(chan outboundFrame, deps.Config.OutboundQueueSize),
		clientTools:      make(map[string]types.Tool),
		toolResultQueue:  make(map[string]chan protocol.ClientToolResult),
	}
	if deps.Hello.Tools != nil {
		for _, tool := range deps.Hello.Tools.ClientTools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			s.clientTools[strings.ToLower(name)] = tool
		}
	}
	s.canceledAssistant.Store(canceledAssistantState{set: make(map[string]struct{}), order: nil})
	return s, nil
}

func (s *LiveSession) Run() error {
	defer s.cancel()

	if s.cfg.MaxJSONMessageBytes > 0 {
		s.conn.SetReadLimit(s.cfg.MaxJSONMessageBytes)
	}
	if s.cfg.ReadTimeout > 0 {
		_ = s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
		s.conn.SetPongHandler(func(string) error {
			return s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
		})
	}

	sttSession, err := s.stt.NewSession(s.ctx, STTConfig{
		Model:      "ink-whisper",
		Language:   sttLanguageFromHello(s.hello),
		Encoding:   s.hello.AudioIn.Encoding,
		SampleRate: s.hello.AudioIn.SampleRateHz,
	})
	if err != nil {
		_ = s.sendWarning("provider_error", "failed to initialize STT")
		return err
	}
	defer sttSession.Close()

	inboundLimiter := newInboundAudioLimiter(s.now, s.cfg.LiveMaxAudioFPS, s.cfg.LiveMaxAudioBytesPerSecond, s.cfg.LiveInboundBurstSeconds)

	voiceProvider := protocol.VoiceProviderCartesia
	if s.hello.Voice != nil && strings.TrimSpace(s.hello.Voice.Provider) != "" {
		voiceProvider = strings.ToLower(strings.TrimSpace(s.hello.Voice.Provider))
	}

	var elevenConn *elevenLabsLiveConn
	if voiceProvider == protocol.VoiceProviderElevenLabs {
		elevenKey := strings.TrimSpace(byokForVoiceProvider(s.hello.BYOK, protocol.VoiceProviderElevenLabs))
		if elevenKey == "" {
			_ = s.sendWarning("unauthorized", "missing elevenlabs key")
			return fmt.Errorf("missing elevenlabs key")
		}
		eleven, err := newElevenLabsLiveConn(s.ctx, elevenLabsLiveConfig{
			APIKey:     elevenKey,
			VoiceID:    strings.TrimSpace(s.hello.Voice.VoiceID),
			BaseWSURL:  strings.TrimSpace(s.cfg.ElevenLabsWSBaseURL),
			AudioOutHz: s.hello.AudioOut.SampleRateHz,
		})
		if err != nil {
			_ = s.sendWarning("provider_error", "failed to initialize elevenlabs tts")
			return err
		}
		elevenConn = eleven
		defer elevenConn.Close()
	}

	readCh := make(chan inboundFrame, 64)
	writerErrCh := make(chan error, 1)
	go s.readLoop(readCh)
	go func() {
		w := outboundWriter{
			ws:         s.conn,
			ctx:        s.ctx,
			cfg:        s.cfg,
			priority:   s.outboundPriority,
			normal:     s.outboundNormal,
			isCanceled: s.isAssistantCanceled,
		}
		writerErrCh <- w.Run()
		close(writerErrCh)
	}()

	flushAndClose := func() error {
		s.cancel()
		wait := 100 * time.Millisecond
		if s.cfg.WriteTimeout > 0 && s.cfg.WriteTimeout < wait {
			wait = s.cfg.WriteTimeout
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-writerErrCh:
		case <-timer.C:
		}
		return nil
	}

	runResultCh := make(chan runResult, 4)
	ttsDoneCh := make(chan ttsResult, 4)
	assistantStartCh := make(chan assistantStart, 4)

	var wg sync.WaitGroup
	defer wg.Wait()

	var (
		silenceTimer      *time.Timer
		silenceActive     bool
		silenceDeadlineMS int64
		graceTimer        *time.Timer
		graceActive       bool
		graceDeadlineMS   int64
		replaceUser       bool
		replacePrefix     string
		currentUtterID    string
		currentText       string
		lastMeaningful    string
		hasConfirmed      bool

		history = newHistoryManager()
		turnID  int

		activeUserCanonicalIdx = -1
		activeUserPlayedIdx    = -1

		activeRunCancel       context.CancelFunc
		activeTTSCancel       context.CancelFunc
		activeTurnInterrupted bool
		activeAssistantID     string
		activeSegment         *speechSegment
		pendingFinalize       *speechSegment
		pendingFinalizeAt     time.Time
		utteranceCounter      int64
		inboundSeq            int64
		binaryStreamStarted   bool
		playbackMarks         = make(map[string]protocol.ClientPlaybackMark)
		backpressureResets    []time.Time
	)

	history.seed(s.hello.Messages)

	onSendErr := func(err error, assistantID string) error {
		if err == nil {
			return nil
		}
		if errors.Is(err, errBackpressure) {
			return s.handleBackpressure(assistantID, &activeTTSCancel, &activeRunCancel)
		}
		return err
	}

	recordBackpressureReset := func() bool {
		now := s.now()
		cutoff := now.Add(-1 * time.Minute)
		filtered := backpressureResets[:0]
		for _, t := range backpressureResets {
			if t.After(cutoff) {
				filtered = append(filtered, t)
			}
		}
		backpressureResets = filtered
		backpressureResets = append(backpressureResets, now)
		return len(backpressureResets) <= s.cfg.MaxBackpressurePerMin
	}

	stopTimer := func(t **time.Timer, active *bool) {
		if *t == nil {
			return
		}
		if !(*t).Stop() {
			select {
			case <-(*t).C:
			default:
			}
		}
		*active = false
	}
	resetTimer := func(t **time.Timer, active *bool, d time.Duration) {
		if d < 0 {
			return
		}
		if *t == nil {
			*t = time.NewTimer(d)
			*active = true
			return
		}
		if !(*t).Stop() {
			select {
			case <-(*t).C:
			default:
			}
		}
		(*t).Reset(d)
		*active = true
	}
	durationUntilSessionDeadline := func(deadlineMS int64) time.Duration {
		if deadlineMS <= 0 {
			return 0
		}
		delta := deadlineMS - s.sessionTimeMS()
		if delta <= 0 {
			return 0
		}
		return time.Duration(delta) * time.Millisecond
	}
	silenceCh := func() <-chan time.Time {
		if !silenceActive || silenceTimer == nil {
			return nil
		}
		return silenceTimer.C
	}
	graceCh := func() <-chan time.Time {
		if !graceActive || graceTimer == nil {
			return nil
		}
		return graceTimer.C
	}

	nextUtteranceID := func() string {
		utteranceCounter++
		return fmt.Sprintf("u_%d", utteranceCounter)
	}

	commitTurnNoAssistant := func() {
		activeTurnInterrupted = false
		activeAssistantID = ""
	}

	finalizePendingPlayed := func(force bool) {
		if pendingFinalize == nil {
			return
		}
		if !force && !pendingFinalize.shouldFinalizeFromMark() && s.now().Before(pendingFinalizeAt) {
			return
		}
		if played := pendingFinalize.playedPrefix(s.hello.AudioOut.SampleRateHz); played != "" {
			history.appendAssistantPlayed(played)
		}
		pendingFinalize = nil
		pendingFinalizeAt = time.Time{}
	}

	interrupt := func(reason string) error {
		oldID := strings.TrimSpace(activeAssistantID)
		if oldID != "" {
			s.cancelAssistantAudio(oldID)
			if err := s.sendAudioReset(reason, oldID); err != nil {
				return onSendErr(err, oldID)
			}
		}
		if activeTTSCancel != nil {
			activeTTSCancel()
			activeTTSCancel = nil
		}
		if activeRunCancel != nil {
			activeRunCancel()
			activeRunCancel = nil
		}
		activeTurnInterrupted = true
		if activeSegment != nil {
			pendingFinalize = activeSegment
			pendingFinalizeAt = s.now().Add(s.cfg.PlaybackStopWait)
		}
		activeSegment = nil
		activeAssistantID = ""
		return nil
	}

	startTurn := func(userText string, replace bool) {
		if activeRunCancel != nil {
			activeRunCancel()
			activeRunCancel = nil
		}
		if activeTTSCancel != nil {
			activeTTSCancel()
			activeTTSCancel = nil
		}
		turnID++
		activeTurnInterrupted = false
		if replace {
			history.replaceCanonicalUser(activeUserCanonicalIdx, userText)
			history.replacePlayedUser(activeUserPlayedIdx, userText)
		} else {
			activeUserCanonicalIdx, activeUserPlayedIdx = history.appendUser(userText)
		}

		historyCopy := history.playedSnapshot()

		runCtx, cancel := s.newTurnContext()
		activeRunCancel = cancel
		currentTurnID := turnID
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, runErr := s.runTurn(runCtx, historyCopy, currentTurnID, runTurnDeps{
				assistantStartCh: assistantStartCh,
				ttsDoneCh:        ttsDoneCh,
				voiceProvider:    voiceProvider,
				elevenConn:       elevenConn,
			})
			select {
			case runResultCh <- runResult{turnID: currentTurnID, text: res.text, assistantID: res.assistantID, segment: res.segment, err: runErr}:
			case <-s.ctx.Done():
			}
		}()
	}

	commitUtterance := func() error {
		trimmed := normalizeSpace(currentText)
		if trimmed == "" || !hasConfirmed {
			return nil
		}
		if replaceUser {
			trimmed = normalizeSpace(strings.TrimSpace(replacePrefix + " " + trimmed))
		}

		if currentUtterID == "" {
			currentUtterID = nextUtteranceID()
		}
		if err := s.sendJSON(protocol.ServerUtteranceFinal{
			Type:        "utterance_final",
			UtteranceID: currentUtterID,
			Text:        trimmed,
			EndMS:       s.sessionTimeMS(),
		}); err != nil {
			return onSendErr(err, activeAssistantID)
		}
		_ = sttSession.FinalizeUtterance()

		startTurn(trimmed, replaceUser)
		graceDeadlineMS = s.sessionTimeMS() + int64(s.cfg.GracePeriod/time.Millisecond)
		resetTimer(&graceTimer, &graceActive, durationUntilSessionDeadline(graceDeadlineMS))
		replaceUser = false
		replacePrefix = ""
		currentUtterID = ""
		currentText = ""
		lastMeaningful = ""
		hasConfirmed = false
		silenceDeadlineMS = 0
		stopTimer(&silenceTimer, &silenceActive)
		return nil
	}

	if s.cfg.MaxSessionDuration > 0 {
		defer func() {
			if graceTimer != nil {
				graceTimer.Stop()
			}
			if silenceTimer != nil {
				silenceTimer.Stop()
			}
		}()
	}

	var sessionTimer *time.Timer
	if s.cfg.MaxSessionDuration > 0 {
		sessionTimer = time.NewTimer(s.cfg.MaxSessionDuration)
		defer sessionTimer.Stop()
	}
	sessionTimerCh := func() <-chan time.Time {
		if sessionTimer == nil {
			return nil
		}
		return sessionTimer.C
	}

	for {
		finalizePendingPlayed(false)
		select {
		case <-s.ctx.Done():
			finalizePendingPlayed(true)
			return nil
		case err := <-writerErrCh:
			if err == nil {
				finalizePendingPlayed(true)
				return nil
			}
			return err
		case frame, ok := <-readCh:
			if !ok {
				return nil
			}
			if frame.err != nil {
				return nil
			}
			switch frame.messageType {
			case websocket.TextMessage:
				msg, decErr := protocol.DecodeClientMessage(frame.data)
				if decErr != nil {
					code := "bad_request"
					if de, ok := decErr.(*protocol.DecodeError); ok {
						code = de.Code
					}
					if err := s.sendSessionError(code, decErr.Error(), true, nil); err != nil {
						return onSendErr(err, activeAssistantID)
					}
					return flushAndClose()
				}
				switch m := msg.(type) {
				case protocol.ClientAudioFrame:
					audio, err := base64.StdEncoding.DecodeString(m.DataB64)
					if err != nil {
						if err := s.sendSessionError("bad_request", "invalid audio_frame.data_b64", true, nil); err != nil {
							return onSendErr(err, activeAssistantID)
						}
						return flushAndClose()
					}
					if len(audio) > s.cfg.MaxAudioFrameBytes {
						if err := s.sendSessionError("bad_request", "audio frame exceeds max size", true, nil); err != nil {
							return onSendErr(err, activeAssistantID)
						}
						return flushAndClose()
					}
					if inboundLimiter != nil && !inboundLimiter.Allow(len(audio)) {
						details := map[string]any{
							"limit_fps":             s.cfg.LiveMaxAudioFPS,
							"limit_bps":             s.cfg.LiveMaxAudioBytesPerSecond,
							"inbound_burst_seconds": s.cfg.LiveInboundBurstSeconds,
						}
						if err := s.sendSessionError("rate_limited", "inbound audio rate limit exceeded", true, details); err != nil {
							return onSendErr(err, activeAssistantID)
						}
						return flushAndClose()
					}
					if err := sttSession.SendAudio(audio); err != nil {
						_ = s.sendWarning("provider_error", "failed to forward audio frame")
						return err
					}
					if m.TimestampMS != nil {
						s.observeClientTimestampMS(*m.TimestampMS)
					}
					inboundSeq++
					if m.Seq > 0 {
						inboundSeq = m.Seq
					}
					if s.cfg.AudioInAckEveryN > 0 && inboundSeq%int64(s.cfg.AudioInAckEveryN) == 0 {
						if err := s.sendJSON(protocol.ServerAudioInAck{Type: "audio_in_ack", StreamID: "mic", LastSeq: inboundSeq, TimestampMS: s.sessionTimeMS()}); err != nil {
							return onSendErr(err, activeAssistantID)
						}
					}
					if silenceActive && silenceDeadlineMS > 0 && s.sessionTimeMS() >= silenceDeadlineMS {
						stopTimer(&silenceTimer, &silenceActive)
						if err := commitUtterance(); err != nil {
							return err
						}
					}
					if graceActive && graceDeadlineMS > 0 && s.sessionTimeMS() >= graceDeadlineMS {
						stopTimer(&graceTimer, &graceActive)
						graceDeadlineMS = 0
					}
				case protocol.ClientAudioStreamStart:
					binaryStreamStarted = true
					if !strings.EqualFold(strings.TrimSpace(m.Encoding), strings.TrimSpace(s.hello.AudioIn.Encoding)) ||
						m.SampleRateHz != s.hello.AudioIn.SampleRateHz ||
						m.Channels != s.hello.AudioIn.Channels {
						if err := s.sendSessionError("unsupported", "audio_stream_start format does not match negotiated audio_in", true, nil); err != nil {
							return onSendErr(err, activeAssistantID)
						}
						return nil
					}
				case protocol.ClientAudioStreamEnd:
					_ = sttSession.FinalizeUtterance()
				case protocol.ClientPlaybackMark:
					playbackMarks[m.AssistantAudioID] = m
					if activeSegment != nil && strings.TrimSpace(activeSegment.id) == strings.TrimSpace(m.AssistantAudioID) {
						activeSegment.updateMark(m)
					}
					if pendingFinalize != nil && strings.TrimSpace(pendingFinalize.id) == strings.TrimSpace(m.AssistantAudioID) {
						pendingFinalize.updateMark(m)
						finalizePendingPlayed(false)
					}
				case protocol.ClientControl:
					switch m.Op {
					case "interrupt":
						if err := interrupt("barge_in"); err != nil {
							return err
						}
					case "cancel_turn":
						if err := interrupt("cancel_turn"); err != nil {
							return err
						}
					case "end_session":
						_ = s.sendWarning("session_end", "session ending by client request")
						return nil
					}
				case protocol.ClientToolResult:
					if !s.dispatchToolResult(m) {
						_ = s.sendWarning("unexpected_tool_result", "received tool_result with no active matching tool call")
					}
				}
			case websocket.BinaryMessage:
				if !s.cfg.AudioTransportBinary {
					if err := s.sendSessionError("bad_request", "binary frames are not negotiated", true, nil); err != nil {
						return onSendErr(err, activeAssistantID)
					}
					return flushAndClose()
				}
				if !binaryStreamStarted {
					if err := s.sendSessionError("bad_request", "audio_stream_start is required before binary audio", true, nil); err != nil {
						return onSendErr(err, activeAssistantID)
					}
					return flushAndClose()
				}
				if len(frame.data) > s.cfg.MaxAudioFrameBytes {
					if err := s.sendSessionError("bad_request", "binary audio frame exceeds max size", true, nil); err != nil {
						return onSendErr(err, activeAssistantID)
					}
					return flushAndClose()
				}
				if inboundLimiter != nil && !inboundLimiter.Allow(len(frame.data)) {
					details := map[string]any{
						"limit_fps":             s.cfg.LiveMaxAudioFPS,
						"limit_bps":             s.cfg.LiveMaxAudioBytesPerSecond,
						"inbound_burst_seconds": s.cfg.LiveInboundBurstSeconds,
					}
					if err := s.sendSessionError("rate_limited", "inbound audio rate limit exceeded", true, details); err != nil {
						return onSendErr(err, activeAssistantID)
					}
					return flushAndClose()
				}
				if err := sttSession.SendAudio(frame.data); err != nil {
					_ = s.sendWarning("provider_error", "failed to forward binary audio")
					return err
				}
				inboundSeq++
				if s.cfg.AudioInAckEveryN > 0 && inboundSeq%int64(s.cfg.AudioInAckEveryN) == 0 {
					if err := s.sendJSON(protocol.ServerAudioInAck{Type: "audio_in_ack", StreamID: "mic", LastSeq: inboundSeq, TimestampMS: s.sessionTimeMS()}); err != nil {
						return onSendErr(err, activeAssistantID)
					}
				}
				if silenceActive && silenceDeadlineMS > 0 && s.sessionTimeMS() >= silenceDeadlineMS {
					stopTimer(&silenceTimer, &silenceActive)
					if err := commitUtterance(); err != nil {
						return err
					}
				}
				if graceActive && graceDeadlineMS > 0 && s.sessionTimeMS() >= graceDeadlineMS {
					stopTimer(&graceTimer, &graceActive)
					graceDeadlineMS = 0
				}
			}
		case delta, ok := <-sttSession.Deltas():
			if !ok {
				return nil
			}
			trimmed := normalizeSpace(delta.Text)
			if trimmed == "" {
				continue
			}
			if currentUtterID == "" {
				currentUtterID = nextUtteranceID()
			}
			if s.hello.Features.WantPartialTranscripts || delta.IsFinal {
				if err := s.sendJSON(protocol.ServerTranscriptDelta{
					Type:        "transcript_delta",
					UtteranceID: currentUtterID,
					IsFinal:     delta.IsFinal,
					Text:        trimmed,
					TimestampMS: s.sessionTimeMS(),
				}); err != nil {
					return onSendErr(err, activeAssistantID)
				}
			}

			if isMeaningfulTranscript(trimmed, lastMeaningful) {
				currentText = trimmed
				lastMeaningful = trimmed
				if IsConfirmedSpeech(trimmed, delta.IsFinal, activeAssistantID != "", s.hello.Features.ClientHasAEC) {
					hasConfirmed = true
				}
				silenceDeadlineMS = s.sessionTimeMS() + int64(s.cfg.SilenceCommit/time.Millisecond)
				resetTimer(&silenceTimer, &silenceActive, durationUntilSessionDeadline(silenceDeadlineMS))
				if graceActive && IsConfirmedSpeech(trimmed, delta.IsFinal, activeAssistantID != "", s.hello.Features.ClientHasAEC) {
					if activeUserPlayedIdx >= 0 && activeUserPlayedIdx < len(history.played) {
						replacePrefix = history.played[activeUserPlayedIdx].TextContent()
						replaceUser = true
					}
					if err := interrupt("barge_in"); err != nil {
						return err
					}
					stopTimer(&graceTimer, &graceActive)
					graceDeadlineMS = 0
				} else if activeAssistantID != "" && IsConfirmedSpeech(trimmed, delta.IsFinal, true, s.hello.Features.ClientHasAEC) {
					if err := interrupt("barge_in"); err != nil {
						return err
					}
				}
			}
		case <-silenceCh():
			if err := commitUtterance(); err != nil {
				return err
			}
		case <-graceCh():
			graceActive = false
			graceDeadlineMS = 0
		case started := <-assistantStartCh:
			if started.turnID != turnID {
				continue
			}
			activeAssistantID = strings.TrimSpace(started.assistantID)
			activeTTSCancel = started.ttsCancel
			activeSegment = started.segment
			if activeSegment != nil {
				if mark, ok := playbackMarks[activeAssistantID]; ok {
					activeSegment.updateMark(mark)
				}
			}
		case rr := <-runResultCh:
			if rr.turnID != turnID {
				continue
			}
			activeRunCancel = nil
			if rr.err != nil {
				if errors.Is(rr.err, context.Canceled) || activeTurnInterrupted {
					continue
				}
				if errors.Is(rr.err, context.DeadlineExceeded) {
					_ = s.sendWarning("turn_timeout", "language model turn timed out")
					commitTurnNoAssistant()
					continue
				}
				_ = s.sendWarning("provider_error", "language model turn failed")
				apology := "Sorry, I ran into an issue with that request. Please try again."
				assistantID := s.nextAssistantID()
				activeAssistantID = assistantID
				history.appendAssistantCanonical(apology)
				seg := newSpeechSegment(assistantID, apology)
				activeSegment = seg
				ttsCtx, cancel := context.WithCancel(s.ctx)
				activeTTSCancel = cancel
				wg.Add(1)
				go func(turn int, aid, speakText string, segment *speechSegment) {
					defer wg.Done()
					s.speakTurn(ttsCtx, turn, aid, speakText, segment, voiceProvider, elevenConn, ttsDoneCh)
				}(turnID, assistantID, apology, seg)
				continue
			}
			text := normalizeSpace(rr.text)
			if text == "" || activeTurnInterrupted {
				commitTurnNoAssistant()
				continue
			}
			if strings.TrimSpace(rr.assistantID) != "" {
				history.appendAssistantCanonical(text)
				if activeAssistantID == "" {
					activeAssistantID = strings.TrimSpace(rr.assistantID)
				}
				if activeSegment == nil && rr.segment != nil {
					activeSegment = rr.segment
				}
				if activeSegment != nil && activeSegment.id == strings.TrimSpace(rr.assistantID) {
					activeSegment.setFullText(text)
				}
				continue
			}

			assistantID := s.nextAssistantID()
			activeAssistantID = assistantID
			history.appendAssistantCanonical(text)
			seg := newSpeechSegment(assistantID, text)
			if mark, ok := playbackMarks[assistantID]; ok {
				seg.updateMark(mark)
			}
			activeSegment = seg
			ttsCtx, cancel := context.WithCancel(s.ctx)
			activeTTSCancel = cancel
			wg.Add(1)
			go func(turn int, aid, speakText string, segment *speechSegment) {
				defer wg.Done()
				s.speakTurn(ttsCtx, turn, aid, speakText, segment, voiceProvider, elevenConn, ttsDoneCh)
			}(turnID, assistantID, text, seg)
		case tr := <-ttsDoneCh:
			if tr.turnID != turnID {
				continue
			}
			activeTTSCancel = nil
			if tr.err != nil {
				if !tr.canceled {
					msg := strings.TrimSpace(tr.err.Error())
					msg = strings.ReplaceAll(msg, "\n", " ")
					msg = strings.ReplaceAll(msg, "\r", " ")
					msg = strings.Join(strings.Fields(msg), " ")
					if len(msg) > 240 {
						msg = msg[:240] + "…"
					}
					if msg == "" {
						msg = "tts stream failed"
					} else {
						msg = "tts stream failed: " + msg
					}
					_ = s.sendWarning("provider_error", msg)
				}
				if errors.Is(tr.err, errAudioWindowBackpressure) || errors.Is(tr.err, errBackpressure) {
					if err := s.handleBackpressure(tr.assistantID, &activeTTSCancel, &activeRunCancel); err != nil {
						return err
					}
					if !recordBackpressureReset() {
						_ = s.sendSessionError("rate_limited", "client cannot keep up with audio playback", true, nil)
						return nil
					}
					if activeSegment != nil && activeSegment.id == tr.assistantID {
						pendingFinalize = activeSegment
						pendingFinalizeAt = s.now().Add(s.cfg.PlaybackStopWait)
						activeSegment = nil
					}
					activeAssistantID = ""
					continue
				}
			}
			if tr.completed && !tr.canceled && !activeTurnInterrupted {
				history.appendAssistantPlayed(tr.text)
				if pendingFinalize != nil && pendingFinalize.id == tr.assistantID {
					pendingFinalize = nil
					pendingFinalizeAt = time.Time{}
				}
			}
			// If the assistant fully finished speaking during the grace window, treat any subsequent
			// speech as a new turn (do not append-on-resume). This avoids rewriting a completed
			// conversation turn after the user already heard the full assistant response.
			if tr.completed && !tr.canceled && graceActive {
				stopTimer(&graceTimer, &graceActive)
				graceDeadlineMS = 0
			}
			if activeSegment != nil && activeSegment.id == tr.assistantID {
				activeSegment = nil
			}
			activeAssistantID = ""
		case <-sessionTimerCh():
			_ = s.sendWarning("session_timeout", "maximum session duration reached")
			finalizePendingPlayed(true)
			return nil
		}
	}
}

func (s *LiveSession) runTurn(ctx context.Context, history []types.Message, turnID int, deps runTurnDeps) (runTurnResult, error) {
	turnHistory := make([]types.Message, len(history))
	copy(turnHistory, history)

	maxModelCalls := s.cfg.MaxModelCallsPerTurn
	if maxModelCalls <= 0 {
		maxModelCalls = 8
	}
	maxToolCalls := s.cfg.MaxToolCallsPerTurn
	if maxToolCalls <= 0 {
		maxToolCalls = 5
	}
	toolTimeoutDefault := s.cfg.ToolTimeout
	if toolTimeoutDefault <= 0 {
		toolTimeoutDefault = 10 * time.Second
	}

	modelCalls := 0
	toolCalls := 0
	stepIndex := 0

	if s.hello.Features.WantRunEvents {
		_ = s.emitRunEvent(turnID, types.RunStartEvent{
			Type:            "run_start",
			RequestID:       s.requestID,
			Model:           s.hello.Model,
			ProtocolVersion: protocol.ProtocolVersion1,
		})
	}

	for {
		if modelCalls >= maxModelCalls {
			_ = s.emitRunEvent(turnID, types.RunErrorEvent{
				Type: "error",
				Error: types.Error{
					Type:    string(core.ErrInvalidRequest),
					Message: "max model calls per turn exceeded",
					Code:    "model_budget_exceeded",
				},
			})
			return runTurnResult{}, fmt.Errorf("max model calls exceeded")
		}

		req := &types.MessageRequest{
			Model:    s.modelName,
			Messages: turnHistory,
			System:   s.turnSystemPrompt(),
			Tools:    s.turnTools(),
			Stream:   true,
		}
		modelCalls++
		if s.hello.Features.WantRunEvents {
			_ = s.emitRunEvent(turnID, types.RunStepStartEvent{Type: "step_start", Index: stepIndex})
		}

		talk, resp, err := s.streamTurnWithStreamingTalk(ctx, req, turnID, deps)
		if err != nil {
			_ = s.emitRunEvent(turnID, types.RunErrorEvent{Type: "error", Error: types.Error{
				Type:      string(core.ErrAPI),
				Message:   err.Error(),
				RequestID: s.requestID,
			}})
			return runTurnResult{}, err
		}
		if talk != nil {
			_ = s.emitRunEvent(turnID, types.RunCompleteEvent{
				Type: "run_complete",
				Result: &types.RunResult{
					StopReason: types.RunStopReasonEndTurn,
				},
			})
			return runTurnResult{text: normalizeSpace(talk.text), assistantID: talk.assistantID, segment: talk.segment}, nil
		}
		if resp == nil {
			_ = s.emitRunEvent(turnID, types.RunCompleteEvent{
				Type: "run_complete",
				Result: &types.RunResult{
					StopReason: types.RunStopReasonEndTurn,
				},
			})
			return runTurnResult{}, nil
		}
		_ = s.emitRunEvent(turnID, types.RunStepCompleteEvent{Type: "step_complete", Index: stepIndex, Response: resp})

		toolUses := resp.ToolUses()
		if resp.StopReason != types.StopReasonToolUse || len(toolUses) == 0 {
			_ = s.emitRunEvent(turnID, types.RunCompleteEvent{
				Type: "run_complete",
				Result: &types.RunResult{
					Response:   resp,
					StopReason: types.RunStopReasonEndTurn,
				},
			})
			return runTurnResult{text: normalizeSpace(resp.TextContent())}, nil
		}

		talkCalls := make([]types.ToolUseBlock, 0, 1)
		toolCallsForExecution := make([]types.ToolUseBlock, 0, len(toolUses))
		for _, call := range toolUses {
			name := strings.TrimSpace(call.Name)
			if strings.EqualFold(name, talkToUserToolName) {
				talkCalls = append(talkCalls, call)
				continue
			}
			toolCallsForExecution = append(toolCallsForExecution, call)
		}

		if len(talkCalls) > 0 && len(toolCallsForExecution) > 0 {
			return runTurnResult{}, fmt.Errorf("invalid tool plan: talk_to_user cannot be combined with other tool calls")
		}
		if len(talkCalls) > 0 {
			text, ok := extractTalkToUserText(talkCalls[0].Input)
			if !ok {
				return runTurnResult{}, fmt.Errorf("talk_to_user.text is required")
			}
			_ = s.emitRunEvent(turnID, types.RunCompleteEvent{
				Type: "run_complete",
				Result: &types.RunResult{
					Response:   resp,
					StopReason: types.RunStopReasonEndTurn,
				},
			})
			return runTurnResult{text: normalizeSpace(text)}, nil
		}
		if len(toolCallsForExecution) == 0 {
			_ = s.emitRunEvent(turnID, types.RunCompleteEvent{
				Type: "run_complete",
				Result: &types.RunResult{
					Response:   resp,
					StopReason: types.RunStopReasonEndTurn,
				},
			})
			return runTurnResult{text: normalizeSpace(resp.TextContent())}, nil
		}

		if toolCalls+len(toolCallsForExecution) > maxToolCalls {
			_ = s.emitRunEvent(turnID, types.RunErrorEvent{Type: "error", Error: types.Error{
				Type:    string(core.ErrInvalidRequest),
				Message: "max tool calls per turn exceeded",
				Code:    "tool_budget_exceeded",
			}})
			return runTurnResult{}, fmt.Errorf("max tool calls exceeded")
		}

		toolResultBlocks := make([]types.ContentBlock, 0, len(toolCallsForExecution))
		for _, call := range toolCallsForExecution {
			callName := strings.TrimSpace(call.Name)
			if callName == "" {
				return runTurnResult{}, fmt.Errorf("tool name is required")
			}
			_ = s.emitRunEvent(turnID, types.RunToolCallStartEvent{
				Type:  "tool_call_start",
				ID:    call.ID,
				Name:  callName,
				Input: call.Input,
			})

			toolCtx := ctx
			cancelTool := func() {}
			toolTimeout := toolTimeoutDefault
			if deadline, ok := ctx.Deadline(); ok {
				remaining := time.Until(deadline)
				if remaining <= 0 {
					return runTurnResult{}, context.DeadlineExceeded
				}
				if toolTimeout <= 0 || remaining < toolTimeout {
					toolTimeout = remaining
				}
			}
			if toolTimeout > 0 {
				var cancel context.CancelFunc
				toolCtx, cancel = context.WithTimeout(ctx, toolTimeout)
				cancelTool = cancel
			}

			var (
				content []types.ContentBlock
				toolErr *types.Error
				execErr error
			)
			switch {
			case s.serverTools != nil && s.serverTools.Has(callName):
				content, toolErr = s.serverTools.Execute(toolCtx, callName, call.Input)
			case s.hasClientTool(callName):
				content, toolErr, execErr = s.executeClientToolCall(toolCtx, ctx, turnID, call)
			default:
				cancelTool()
				return runTurnResult{}, fmt.Errorf("unknown tool %q", callName)
			}
			cancelTool()
			if execErr != nil {
				return runTurnResult{}, execErr
			}

			block := types.ToolResultBlock{
				Type:      "tool_result",
				ToolUseID: call.ID,
				Content:   content,
			}
			if toolErr != nil {
				block.IsError = true
				if len(block.Content) == 0 {
					msg := strings.TrimSpace(toolErr.Message)
					if msg == "" {
						msg = "tool execution failed"
					}
					block.Content = []types.ContentBlock{types.TextBlock{Type: "text", Text: msg}}
				}
			} else if len(block.Content) == 0 {
				block.Content = []types.ContentBlock{types.TextBlock{Type: "text", Text: ""}}
			}

			toolResultBlocks = append(toolResultBlocks, block)
			_ = s.emitRunEvent(turnID, types.RunToolResultEvent{
				Type:    "tool_result",
				ID:      call.ID,
				Name:    callName,
				Content: block.Content,
				IsError: block.IsError,
				Error:   toolErr,
			})
		}

		toolCalls += len(toolCallsForExecution)
		assistantMsg := types.Message{Role: "assistant", Content: resp.Content}
		toolMsg := types.Message{Role: "user", Content: toolResultBlocks}
		turnHistory = append(turnHistory, assistantMsg, toolMsg)
		_ = s.emitRunEvent(turnID, types.RunHistoryDeltaEvent{
			Type:        "history_delta",
			ExpectedLen: len(turnHistory) - 2,
			Append:      []types.Message{assistantMsg, toolMsg},
		})
		stepIndex++
	}
}

type talkOutcome struct {
	assistantID string
	segment     *speechSegment
	text        string
}

func (s *LiveSession) streamTurnWithStreamingTalk(ctx context.Context, req *types.MessageRequest, turnID int, deps runTurnDeps) (*talkOutcome, *types.MessageResponse, error) {
	// If we don't have the wiring to stream TTS/captions, fall back to the old early-stop behavior.
	if deps.assistantStartCh == nil || deps.ttsDoneCh == nil {
		text, resp, err := s.streamTurnWithEarlyTalk(ctx, req)
		if strings.TrimSpace(text) != "" {
			return &talkOutcome{text: text}, nil, nil
		}
		return nil, resp, err
	}

	turnCtx, stopEarly := context.WithCancel(ctx)
	defer stopEarly()

	stream, err := s.provider.StreamMessage(turnCtx, req)
	if err != nil {
		return nil, nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = stream.Close()
		}
	}()

	acc := runloop.NewStreamAccumulator()

	talkIdx := -1
	var extractor TalkTextExtractor

	var assistantID string
	var segment *speechSegment
	var ttsCancel context.CancelFunc

	appendCh := make(chan string, 64)
	chunkCh := make(chan string, 64)

	var appendClosed atomic.Bool
	closeAppend := func() {
		if appendClosed.Swap(true) {
			return
		}
		close(appendCh)
	}

	startedSpeech := false
	startSpeech := func() error {
		if startedSpeech {
			return nil
		}
		startedSpeech = true

		assistantID = s.nextAssistantID()
		segment = newSpeechSegment(assistantID, "")

		ttsCtx, cancel := context.WithCancel(s.ctx)
		ttsCancel = cancel

		select {
		case deps.assistantStartCh <- assistantStart{
			turnID:      turnID,
			assistantID: assistantID,
			segment:     segment,
			ttsCancel:   ttsCancel,
		}:
		case <-s.ctx.Done():
			return s.ctx.Err()
		}

		chunker := NewTextChunker(DefaultTextChunkConfig(), nil)
		go chunker.Run(ttsCtx, appendCh, chunkCh)
		go s.speakTurnStreaming(ttsCtx, turnID, assistantID, segment, deps.voiceProvider, deps.elevenConn, chunkCh, deps.ttsDoneCh)
		return nil
	}

	sendTextDelta := func(delta string) {
		if delta == "" || assistantID == "" {
			return
		}
		if !s.hello.Features.WantAssistantText {
			return
		}
		_ = s.sendAssistantJSON(assistantID, protocol.ServerAssistantTextDelta{
			Type:             "assistant_text_delta",
			AssistantAudioID: assistantID,
			Delta:            delta,
		})
	}
	sendTextFinal := func(text string) {
		if assistantID == "" {
			return
		}
		if !s.hello.Features.WantAssistantText {
			return
		}
		_ = s.sendAssistantJSON(assistantID, protocol.ServerAssistantTextFinal{
			Type:             "assistant_text_final",
			AssistantAudioID: assistantID,
			Text:             text,
		})
	}

	for {
		event, nextErr := stream.Next()
		if event != nil {
			acc.Apply(event)
			switch e := event.(type) {
			case types.ContentBlockStartEvent:
				if isTalkToUserToolUseBlock(e.ContentBlock) {
					talkIdx = e.Index
					if err := startSpeech(); err != nil {
						return nil, nil, err
					}
				} else if talkIdx >= 0 && isToolUseBlock(e.ContentBlock) {
					return nil, nil, fmt.Errorf("invalid tool plan: talk_to_user cannot be combined with other tool calls")
				}
			case *types.ContentBlockStartEvent:
				if e != nil && isTalkToUserToolUseBlock(e.ContentBlock) {
					talkIdx = e.Index
					if err := startSpeech(); err != nil {
						return nil, nil, err
					}
				} else if e != nil && talkIdx >= 0 && isToolUseBlock(e.ContentBlock) {
					return nil, nil, fmt.Errorf("invalid tool plan: talk_to_user cannot be combined with other tool calls")
				}
			case types.ContentBlockDeltaEvent:
				if e.Index == talkIdx {
					if err := startSpeech(); err != nil {
						return nil, nil, err
					}
					var raw strings.Builder
					appendInputJSONDelta(&raw, e.Delta)
					if raw.Len() > 0 {
						newText, err := extractor.Feed(raw.String())
						if err != nil {
							if ttsCancel != nil {
								ttsCancel()
							}
							return nil, nil, err
						}
						if newText != "" {
							sendTextDelta(newText)
							if segment != nil {
								segment.setFullText(extractor.FullText())
							}
							select {
							case appendCh <- newText:
							case <-s.ctx.Done():
								return nil, nil, s.ctx.Err()
							}
						}
					}
				}
			case *types.ContentBlockDeltaEvent:
				if e != nil && e.Index == talkIdx {
					if err := startSpeech(); err != nil {
						return nil, nil, err
					}
					var raw strings.Builder
					appendInputJSONDelta(&raw, e.Delta)
					if raw.Len() > 0 {
						newText, err := extractor.Feed(raw.String())
						if err != nil {
							if ttsCancel != nil {
								ttsCancel()
							}
							return nil, nil, err
						}
						if newText != "" {
							sendTextDelta(newText)
							if segment != nil {
								segment.setFullText(extractor.FullText())
							}
							select {
							case appendCh <- newText:
							case <-s.ctx.Done():
								return nil, nil, s.ctx.Err()
							}
						}
					}
				}
			case types.ContentBlockStopEvent:
				if e.Index == talkIdx {
					finalText := extractor.FullText()
					// Fallback: if we didn't decode incrementally, try the accumulated tool input.
					if strings.TrimSpace(finalText) == "" {
						if resp := acc.Response(); resp != nil {
							for _, call := range resp.ToolUses() {
								if strings.EqualFold(strings.TrimSpace(call.Name), talkToUserToolName) {
									if text, ok := extractTalkToUserText(call.Input); ok {
										finalText = text
										sendTextDelta(text)
										select {
										case appendCh <- text:
										case <-s.ctx.Done():
											return nil, nil, s.ctx.Err()
										}
									}
								}
							}
						}
					}
					if strings.TrimSpace(finalText) == "" {
						if ttsCancel != nil {
							ttsCancel()
						}
						closeAppend()
						return nil, nil, fmt.Errorf("talk_to_user.text is required")
					}
					if segment != nil {
						segment.setFullText(finalText)
					}
					sendTextFinal(finalText)
					closeAppend()
					stopEarly()
					closed = true
					_ = stream.Close()
					return &talkOutcome{assistantID: assistantID, segment: segment, text: finalText}, nil, nil
				}
			case *types.ContentBlockStopEvent:
				if e != nil && e.Index == talkIdx {
					finalText := extractor.FullText()
					if strings.TrimSpace(finalText) == "" {
						if resp := acc.Response(); resp != nil {
							for _, call := range resp.ToolUses() {
								if strings.EqualFold(strings.TrimSpace(call.Name), talkToUserToolName) {
									if text, ok := extractTalkToUserText(call.Input); ok {
										finalText = text
										sendTextDelta(text)
										select {
										case appendCh <- text:
										case <-s.ctx.Done():
											return nil, nil, s.ctx.Err()
										}
									}
								}
							}
						}
					}
					if strings.TrimSpace(finalText) == "" {
						if ttsCancel != nil {
							ttsCancel()
						}
						closeAppend()
						return nil, nil, fmt.Errorf("talk_to_user.text is required")
					}
					if segment != nil {
						segment.setFullText(finalText)
					}
					sendTextFinal(finalText)
					closeAppend()
					stopEarly()
					closed = true
					_ = stream.Close()
					return &talkOutcome{assistantID: assistantID, segment: segment, text: finalText}, nil, nil
				}
			case types.ErrorEvent:
				if ttsCancel != nil {
					ttsCancel()
				}
				closeAppend()
				return nil, nil, fmt.Errorf("provider stream error: %s", strings.TrimSpace(e.Error.Message))
			case *types.ErrorEvent:
				if e != nil {
					if ttsCancel != nil {
						ttsCancel()
					}
					closeAppend()
					return nil, nil, fmt.Errorf("provider stream error: %s", strings.TrimSpace(e.Error.Message))
				}
			}
		}
		if nextErr != nil {
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if ttsCancel != nil {
				ttsCancel()
			}
			closeAppend()
			return nil, nil, nextErr
		}
	}

	closeAppend()
	return nil, acc.Response(), nil
}

func isToolUseBlock(block types.ContentBlock) bool {
	switch block.(type) {
	case types.ToolUseBlock, *types.ToolUseBlock, types.ServerToolUseBlock, *types.ServerToolUseBlock:
		return true
	default:
		return false
	}
}

func (s *LiveSession) streamTurnWithEarlyTalk(ctx context.Context, req *types.MessageRequest) (string, *types.MessageResponse, error) {
	turnCtx, stopEarly := context.WithCancel(ctx)
	defer stopEarly()

	stream, err := s.provider.StreamMessage(turnCtx, req)
	if err != nil {
		return "", nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = stream.Close()
		}
	}()

	talkIdx := -1
	var talkInput strings.Builder
	acc := runloop.NewStreamAccumulator()

	for {
		event, nextErr := stream.Next()
		if event != nil {
			acc.Apply(event)
			switch e := event.(type) {
			case types.ContentBlockStartEvent:
				if isTalkToUserToolUseBlock(e.ContentBlock) {
					talkIdx = e.Index
					talkInput.Reset()
				}
			case *types.ContentBlockStartEvent:
				if e != nil && isTalkToUserToolUseBlock(e.ContentBlock) {
					talkIdx = e.Index
					talkInput.Reset()
				}
			case types.ContentBlockDeltaEvent:
				if e.Index == talkIdx {
					appendInputJSONDelta(&talkInput, e.Delta)
				}
			case *types.ContentBlockDeltaEvent:
				if e != nil && e.Index == talkIdx {
					appendInputJSONDelta(&talkInput, e.Delta)
				}
			case types.ContentBlockStopEvent:
				if e.Index == talkIdx {
					if text, ok := parseTalkToUserText(talkInput.String()); ok {
						stopEarly()
						closed = true
						_ = stream.Close()
						return text, nil, nil
					}
					talkIdx = -1
					talkInput.Reset()
				}
			case *types.ContentBlockStopEvent:
				if e != nil && e.Index == talkIdx {
					if text, ok := parseTalkToUserText(talkInput.String()); ok {
						stopEarly()
						closed = true
						_ = stream.Close()
						return text, nil, nil
					}
					talkIdx = -1
					talkInput.Reset()
				}
			case types.ErrorEvent:
				return "", nil, fmt.Errorf("provider stream error: %s", strings.TrimSpace(e.Error.Message))
			case *types.ErrorEvent:
				if e != nil {
					return "", nil, fmt.Errorf("provider stream error: %s", strings.TrimSpace(e.Error.Message))
				}
			}
		}
		if nextErr != nil {
			if errors.Is(nextErr, io.EOF) {
				break
			}
			return "", nil, nextErr
		}
	}

	return "", acc.Response(), nil
}

func (s *LiveSession) turnTools() []types.Tool {
	additionalProps := false
	tools := []types.Tool{
		{
			Type:        types.ToolTypeFunction,
			Name:        talkToUserToolName,
			Description: "Speak text to the end user.",
			InputSchema: &types.JSONSchema{
				Type: "object",
				Properties: map[string]types.JSONSchema{
					"text": {
						Type:        "string",
						Description: "Text to speak to the user. No markdown.",
					},
				},
				Required:             []string{"text"},
				AdditionalProperties: &additionalProps,
			},
		},
	}
	appendClientTools := func() {
		if len(s.clientTools) == 0 {
			return
		}
		names := make([]string, 0, len(s.clientTools))
		for name := range s.clientTools {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			tools = append(tools, s.clientTools[name])
		}
	}

	if s.serverTools == nil {
		appendClientTools()
		return tools
	}
	for _, name := range s.serverTools.Names() {
		if def, ok := s.serverTools.Definition(name); ok {
			tools = append(tools, def)
		}
	}
	appendClientTools()
	return tools
}

func (s *LiveSession) hasClientTool(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.clientTools[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func (s *LiveSession) turnSystemPrompt() string {
	extra := strings.TrimSpace(s.hello.System)
	if extra == "" {
		return voiceSystemPrompt
	}
	return voiceSystemPrompt + "\n\n" + extra
}

func extractTalkToUserText(input map[string]any) (string, bool) {
	if input == nil {
		return "", false
	}
	raw, ok := input["text"]
	if !ok {
		return "", false
	}
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func isTalkToUserToolUseBlock(block types.ContentBlock) bool {
	switch b := block.(type) {
	case types.ToolUseBlock:
		return strings.EqualFold(strings.TrimSpace(b.Name), talkToUserToolName)
	case *types.ToolUseBlock:
		return b != nil && strings.EqualFold(strings.TrimSpace(b.Name), talkToUserToolName)
	case types.ServerToolUseBlock:
		return strings.EqualFold(strings.TrimSpace(b.Name), talkToUserToolName)
	case *types.ServerToolUseBlock:
		return b != nil && strings.EqualFold(strings.TrimSpace(b.Name), talkToUserToolName)
	default:
		return false
	}
}

func appendInputJSONDelta(buf *strings.Builder, delta types.Delta) {
	switch d := delta.(type) {
	case types.InputJSONDelta:
		buf.WriteString(d.PartialJSON)
	case *types.InputJSONDelta:
		if d != nil {
			buf.WriteString(d.PartialJSON)
		}
	}
}

func parseTalkToUserText(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", false
	}
	text := strings.TrimSpace(parsed.Text)
	if text == "" {
		return "", false
	}
	return text, true
}

func (s *LiveSession) speakTurn(
	ctx context.Context,
	turnID int,
	assistantID, text string,
	segment *speechSegment,
	voiceProvider string,
	elevenConn *elevenLabsLiveConn,
	out chan<- ttsResult,
) {
	res := ttsResult{turnID: turnID, assistantID: assistantID, text: text}
	defer func() {
		select {
		case out <- res:
		case <-s.ctx.Done():
		}
	}()

	startMsg := protocol.ServerAssistantAudioStart{
		Type:             "assistant_audio_start",
		AssistantAudioID: assistantID,
		Format: protocol.AudioFormat{
			Encoding:     s.hello.AudioOut.Encoding,
			SampleRateHz: s.hello.AudioOut.SampleRateHz,
			Channels:     s.hello.AudioOut.Channels,
		},
	}
	if s.hello.Features.WantAssistantText {
		startMsg.Text = text
	}
	if err := s.sendAssistantJSON(assistantID, startMsg); err != nil {
		res.err = err
		return
	}

	shouldBackpressureReset := func() bool {
		// Playback-mark windowing only makes sense when the client is actually sending playback marks.
		// For ElevenLabs we require this at handshake; for other providers it may be disabled.
		if !s.hello.Features.SendPlaybackMarks {
			return false
		}
		if segment == nil {
			return false
		}
		if s.cfg.MaxUnplayedDuration <= 0 {
			return false
		}
		unplayedMS := segment.unplayedMS(s.hello.AudioOut.SampleRateHz)
		return time.Duration(unplayedMS)*time.Millisecond > s.cfg.MaxUnplayedDuration
	}

	// ElevenLabs (and sometimes other providers) can deliver audio faster than realtime. If we forward
	// too far ahead, the gateway will (correctly) enforce the unplayed window and reset. To avoid
	// spurious resets, pace outbound audio by blocking until the client catches up, and split large
	// provider chunks into smaller parts.
	waitForUnplayedWindow := func(chunkBytes int) error {
		if chunkBytes <= 0 {
			return nil
		}
		if !s.hello.Features.SendPlaybackMarks || segment == nil || s.cfg.MaxUnplayedDuration <= 0 {
			return nil
		}

		sampleRate := s.hello.AudioOut.SampleRateHz
		if sampleRate <= 0 {
			sampleRate = 24000
		}
		// PCM16 mono => 2 bytes per sample. (Live v1 negotiates pcm_s16le @ 24kHz mono)
		chunkSamples := int64(chunkBytes / 2)
		chunkMS := (chunkSamples * 1000) / int64(sampleRate)
		if chunkMS <= 0 {
			chunkMS = 1
		}

		maxMS := int64(s.cfg.MaxUnplayedDuration / time.Millisecond)
		if maxMS <= 0 {
			return nil
		}
		initialUnplayedMS := segment.unplayedMS(sampleRate)
		needDropMS := (initialUnplayedMS + chunkMS) - maxMS
		if needDropMS < 0 {
			needDropMS = 0
		}
		// Budget enough wall time for the client to advance `played_ms` in realtime plus a bit of slack,
		// but cap it so we don't stall forever if the client stops sending marks.
		budget := time.Duration(needDropMS+500) * time.Millisecond
		if budget < 2*time.Second {
			budget = 2 * time.Second
		}
		if budget > 10*time.Second {
			budget = 10 * time.Second
		}
		deadline := time.Now().Add(budget)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			unplayedMS := segment.unplayedMS(sampleRate)
			if unplayedMS+chunkMS <= maxMS {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%w (unplayed_ms=%d played_ms=%d max_ms=%d)", errAudioWindowBackpressure, unplayedMS, segment.lastPlayedMS(), maxMS)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	bytesPerSecond := int64(s.hello.AudioOut.SampleRateHz * s.hello.AudioOut.Channels * 2)
	if bytesPerSecond <= 0 {
		bytesPerSecond = int64(24000 * 1 * 2)
	}
	maxPartMS := int64(200)
	maxPartBytes := int((bytesPerSecond * maxPartMS) / 1000)
	if maxPartBytes < 960 {
		maxPartBytes = 960
	}

	if strings.EqualFold(strings.TrimSpace(voiceProvider), protocol.VoiceProviderElevenLabs) {
		if elevenConn == nil {
			res.err = fmt.Errorf("elevenlabs tts connection is not available")
			return
		}
		if err := elevenConn.StartContext(ctx, assistantID); err != nil {
			res.err = err
			return
		}
		if err := elevenConn.SendText(ctx, assistantID, text, true); err != nil {
			res.err = err
			return
		}
		seq := int64(1)
		for {
			select {
			case <-ctx.Done():
				_ = elevenConn.CloseContext(context.Background(), assistantID)
				res.canceled = true
				return
			case chunk, ok := <-elevenConn.Chunks():
				if !ok {
					if err := s.sendAssistantJSON(assistantID, protocol.ServerAssistantAudioEnd{Type: "assistant_audio_end", AssistantAudioID: assistantID}); err != nil {
						res.err = err
						return
					}
					res.completed = true
					return
				}
				if strings.TrimSpace(chunk.ContextID) != assistantID {
					continue
				}
				if len(chunk.Audio) > 0 {
					// When splitting, only attach alignment to the first part (best effort).
					pcm := chunk.Audio
					firstAlign := chunk.Alignment
					if len(pcm) > maxPartBytes {
						firstAlign = nil
					}
					if err := waitForUnplayedWindow(min(len(pcm), maxPartBytes)); err != nil {
						res.err = err
						return
					}
					if segment != nil {
						segment.addChunk(pcm[:min(len(pcm), maxPartBytes)], firstAlign, s.hello.AudioOut.SampleRateHz)
					}
					if err := s.sendAssistantChunk(assistantID, seq, pcm[:min(len(pcm), maxPartBytes)], firstAlign); err != nil {
						res.err = err
						return
					}
					seq++
					if len(pcm) > maxPartBytes {
						// Send remaining parts without alignment.
						for off := maxPartBytes; off < len(pcm); {
							end := off + maxPartBytes
							if end > len(pcm) {
								end = len(pcm)
							}
							part := pcm[off:end]
							off = end
							if err := waitForUnplayedWindow(len(part)); err != nil {
								res.err = err
								return
							}
							if segment != nil {
								segment.addChunk(part, nil, s.hello.AudioOut.SampleRateHz)
							}
							if err := s.sendAssistantChunk(assistantID, seq, part, nil); err != nil {
								res.err = err
								return
							}
							seq++
							if shouldBackpressureReset() {
								res.err = errAudioWindowBackpressure
								return
							}
						}
					}
				}
				if chunk.Final {
					if err := s.sendAssistantJSON(assistantID, protocol.ServerAssistantAudioEnd{Type: "assistant_audio_end", AssistantAudioID: assistantID}); err != nil {
						res.err = err
						return
					}
					res.completed = true
					return
				}
			}
		}
	}

	ttsCtx, err := s.tts.NewContext(ctx, TTSConfig{
		Voice:            strings.TrimSpace(s.hello.Voice.VoiceID),
		Language:         strings.TrimSpace(s.hello.Voice.Language),
		Speed:            s.hello.Voice.Speed,
		Volume:           s.hello.Voice.Volume,
		Emotion:          strings.TrimSpace(s.hello.Voice.Emotion),
		Format:           "pcm",
		SampleRate:       s.hello.AudioOut.SampleRateHz,
		MaxBufferDelayMS: 200,
	})
	if err != nil {
		res.err = err
		return
	}
	defer ttsCtx.Close()

	if err := ttsCtx.SendText(text, false); err != nil {
		res.err = err
		return
	}
	if err := ttsCtx.Flush(); err != nil {
		res.err = err
		return
	}

	seq := int64(1)
	for {
		select {
		case <-ctx.Done():
			res.canceled = true
			return
		case chunk, ok := <-ttsCtx.Audio():
			if !ok {
				if err := ttsCtx.Err(); err != nil && !errors.Is(err, context.Canceled) {
					res.err = err
					return
				}
				if ctx.Err() != nil {
					res.canceled = true
					return
				}
				if err := s.sendAssistantJSON(assistantID, protocol.ServerAssistantAudioEnd{Type: "assistant_audio_end", AssistantAudioID: assistantID}); err != nil {
					res.err = err
					return
				}
				res.completed = true
				return
			}
			if len(chunk) == 0 {
				continue
			}
			// Split and pace.
			for off := 0; off < len(chunk); {
				end := off + maxPartBytes
				if end > len(chunk) {
					end = len(chunk)
				}
				part := chunk[off:end]
				off = end
				if err := waitForUnplayedWindow(len(part)); err != nil {
					res.err = err
					return
				}
				if segment != nil {
					segment.addChunk(part, nil, s.hello.AudioOut.SampleRateHz)
				}
				if err := s.sendAssistantChunk(assistantID, seq, part, nil); err != nil {
					res.err = err
					return
				}
				if shouldBackpressureReset() {
					res.err = errAudioWindowBackpressure
					return
				}
				seq++
			}
		}
	}
}

func (s *LiveSession) speakTurnStreaming(
	ctx context.Context,
	turnID int,
	assistantID string,
	segment *speechSegment,
	voiceProvider string,
	elevenConn *elevenLabsLiveConn,
	textChunks <-chan string,
	out chan<- ttsResult,
) {
	res := ttsResult{turnID: turnID, assistantID: assistantID}
	defer func() {
		select {
		case out <- res:
		case <-s.ctx.Done():
		}
	}()

	startMsg := protocol.ServerAssistantAudioStart{
		Type:             "assistant_audio_start",
		AssistantAudioID: assistantID,
		Format: protocol.AudioFormat{
			Encoding:     s.hello.AudioOut.Encoding,
			SampleRateHz: s.hello.AudioOut.SampleRateHz,
			Channels:     s.hello.AudioOut.Channels,
		},
	}
	if err := s.sendAssistantJSON(assistantID, startMsg); err != nil {
		res.err = err
		return
	}

	shouldBackpressureReset := func() bool {
		if !s.hello.Features.SendPlaybackMarks {
			return false
		}
		if segment == nil {
			return false
		}
		if s.cfg.MaxUnplayedDuration <= 0 {
			return false
		}
		unplayedMS := segment.unplayedMS(s.hello.AudioOut.SampleRateHz)
		return time.Duration(unplayedMS)*time.Millisecond > s.cfg.MaxUnplayedDuration
	}

	waitForUnplayedWindow := func(chunkBytes int) error {
		if chunkBytes <= 0 {
			return nil
		}
		if !s.hello.Features.SendPlaybackMarks || segment == nil || s.cfg.MaxUnplayedDuration <= 0 {
			return nil
		}

		sampleRate := s.hello.AudioOut.SampleRateHz
		if sampleRate <= 0 {
			sampleRate = 24000
		}
		chunkSamples := int64(chunkBytes / 2)
		chunkMS := (chunkSamples * 1000) / int64(sampleRate)
		if chunkMS <= 0 {
			chunkMS = 1
		}

		maxMS := int64(s.cfg.MaxUnplayedDuration / time.Millisecond)
		if maxMS <= 0 {
			return nil
		}
		initialUnplayedMS := segment.unplayedMS(sampleRate)
		needDropMS := (initialUnplayedMS + chunkMS) - maxMS
		if needDropMS < 0 {
			needDropMS = 0
		}
		budget := time.Duration(needDropMS+500) * time.Millisecond
		if budget < 2*time.Second {
			budget = 2 * time.Second
		}
		if budget > 10*time.Second {
			budget = 10 * time.Second
		}
		deadline := time.Now().Add(budget)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			unplayedMS := segment.unplayedMS(sampleRate)
			if unplayedMS+chunkMS <= maxMS {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%w (unplayed_ms=%d played_ms=%d max_ms=%d)", errAudioWindowBackpressure, unplayedMS, segment.lastPlayedMS(), maxMS)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	bytesPerSecond := int64(s.hello.AudioOut.SampleRateHz * s.hello.AudioOut.Channels * 2)
	if bytesPerSecond <= 0 {
		bytesPerSecond = int64(24000 * 1 * 2)
	}
	maxPartMS := int64(200)
	maxPartBytes := int((bytesPerSecond * maxPartMS) / 1000)
	if maxPartBytes < 960 {
		maxPartBytes = 960
	}

	if strings.EqualFold(strings.TrimSpace(voiceProvider), protocol.VoiceProviderElevenLabs) {
		if elevenConn == nil {
			res.err = fmt.Errorf("elevenlabs tts connection is not available")
			return
		}
		if err := elevenConn.StartContext(ctx, assistantID); err != nil {
			res.err = err
			return
		}

		sendErrCh := make(chan error, 1)
		go func() {
			for chunk := range textChunks {
				if ctx.Err() != nil {
					return
				}
				if strings.TrimSpace(chunk) == "" {
					continue
				}
				if err := elevenConn.SendText(ctx, assistantID, chunk, true); err != nil {
					select {
					case sendErrCh <- err:
					default:
					}
					return
				}
			}
			_ = elevenConn.CloseContext(context.Background(), assistantID)
		}()

		seq := int64(1)
		for {
			select {
			case <-ctx.Done():
				_ = elevenConn.CloseContext(context.Background(), assistantID)
				res.canceled = true
				return
			case err := <-sendErrCh:
				if err != nil {
					res.err = err
					return
				}
			case chunk, ok := <-elevenConn.Chunks():
				if !ok {
					if err := s.sendAssistantJSON(assistantID, protocol.ServerAssistantAudioEnd{Type: "assistant_audio_end", AssistantAudioID: assistantID}); err != nil {
						res.err = err
						return
					}
					res.completed = true
					if segment != nil {
						res.text = segment.fullTextSnapshot()
					}
					return
				}
				if strings.TrimSpace(chunk.ContextID) != assistantID {
					continue
				}
				if len(chunk.Audio) > 0 {
					pcm := chunk.Audio
					firstAlign := chunk.Alignment
					if len(pcm) > maxPartBytes {
						firstAlign = nil
					}
					if err := waitForUnplayedWindow(min(len(pcm), maxPartBytes)); err != nil {
						res.err = err
						return
					}
					if segment != nil {
						segment.addChunk(pcm[:min(len(pcm), maxPartBytes)], firstAlign, s.hello.AudioOut.SampleRateHz)
					}
					if err := s.sendAssistantChunk(assistantID, seq, pcm[:min(len(pcm), maxPartBytes)], firstAlign); err != nil {
						res.err = err
						return
					}
					seq++
					if len(pcm) > maxPartBytes {
						for off := maxPartBytes; off < len(pcm); {
							end := off + maxPartBytes
							if end > len(pcm) {
								end = len(pcm)
							}
							part := pcm[off:end]
							off = end
							if err := waitForUnplayedWindow(len(part)); err != nil {
								res.err = err
								return
							}
							if segment != nil {
								segment.addChunk(part, nil, s.hello.AudioOut.SampleRateHz)
							}
							if err := s.sendAssistantChunk(assistantID, seq, part, nil); err != nil {
								res.err = err
								return
							}
							seq++
							if shouldBackpressureReset() {
								res.err = errAudioWindowBackpressure
								return
							}
						}
					}
				}
				if chunk.Final {
					if err := s.sendAssistantJSON(assistantID, protocol.ServerAssistantAudioEnd{Type: "assistant_audio_end", AssistantAudioID: assistantID}); err != nil {
						res.err = err
						return
					}
					res.completed = true
					if segment != nil {
						res.text = segment.fullTextSnapshot()
					}
					return
				}
			}
		}
	}

	ttsCtx, err := s.tts.NewContext(ctx, TTSConfig{
		Voice:            strings.TrimSpace(s.hello.Voice.VoiceID),
		Language:         strings.TrimSpace(s.hello.Voice.Language),
		Speed:            s.hello.Voice.Speed,
		Volume:           s.hello.Voice.Volume,
		Emotion:          strings.TrimSpace(s.hello.Voice.Emotion),
		Format:           "pcm",
		SampleRate:       s.hello.AudioOut.SampleRateHz,
		MaxBufferDelayMS: 200,
	})
	if err != nil {
		res.err = err
		return
	}
	defer ttsCtx.Close()

	sendErrCh := make(chan error, 1)
	go func() {
		for chunk := range textChunks {
			if ctx.Err() != nil {
				return
			}
			if strings.TrimSpace(chunk) == "" {
				continue
			}
			if err := ttsCtx.SendText(chunk, false); err != nil {
				select {
				case sendErrCh <- err:
				default:
				}
				return
			}
		}
		if err := ttsCtx.Flush(); err != nil {
			select {
			case sendErrCh <- err:
			default:
			}
			return
		}
	}()

	seq := int64(1)
	for {
		select {
		case <-ctx.Done():
			res.canceled = true
			return
		case err := <-sendErrCh:
			if err != nil {
				res.err = err
				return
			}
		case chunk, ok := <-ttsCtx.Audio():
			if !ok {
				if err := ttsCtx.Err(); err != nil && !errors.Is(err, context.Canceled) {
					res.err = err
					return
				}
				if ctx.Err() != nil {
					res.canceled = true
					return
				}
				if err := s.sendAssistantJSON(assistantID, protocol.ServerAssistantAudioEnd{Type: "assistant_audio_end", AssistantAudioID: assistantID}); err != nil {
					res.err = err
					return
				}
				res.completed = true
				if segment != nil {
					res.text = segment.fullTextSnapshot()
				}
				return
			}
			if len(chunk) == 0 {
				continue
			}
			for off := 0; off < len(chunk); {
				end := off + maxPartBytes
				if end > len(chunk) {
					end = len(chunk)
				}
				part := chunk[off:end]
				off = end
				if err := waitForUnplayedWindow(len(part)); err != nil {
					res.err = err
					return
				}
				if segment != nil {
					segment.addChunk(part, nil, s.hello.AudioOut.SampleRateHz)
				}
				if err := s.sendAssistantChunk(assistantID, seq, part, nil); err != nil {
					res.err = err
					return
				}
				if shouldBackpressureReset() {
					res.err = errAudioWindowBackpressure
					return
				}
				seq++
			}
		}
	}
}

func (s *LiveSession) sendAssistantChunk(assistantID string, seq int64, chunk []byte, alignment *protocol.Alignment) error {
	if s.cfg.AudioTransportBinary {
		header := protocol.ServerAssistantAudioChunkHeader{
			Type:             "assistant_audio_chunk_header",
			AssistantAudioID: assistantID,
			Seq:              seq,
			Bytes:            len(chunk),
			Alignment:        alignment,
		}
		return s.sendAssistantBinaryPair(assistantID, header, chunk)
	}
	return s.sendAssistantJSON(assistantID, protocol.ServerAssistantAudioChunk{
		Type:             "assistant_audio_chunk",
		AssistantAudioID: assistantID,
		Seq:              seq,
		AudioB64:         base64.StdEncoding.EncodeToString(chunk),
		Alignment:        alignment,
	})
}

func (s *LiveSession) sendAudioReset(reason, assistantID string) error {
	return s.sendJSONPriority(protocol.ServerAudioReset{Type: "audio_reset", Reason: reason, AssistantAudioID: assistantID})
}

func (s *LiveSession) sendWarning(code, message string) error {
	return s.sendJSON(protocol.ServerWarning{Type: "warning", Code: code, Message: message})
}

func (s *LiveSession) sendSessionError(code, message string, close bool, details map[string]any) error {
	msg := protocol.ServerError{Type: "error", Scope: "session", Code: code, Message: message, Close: close, Details: details}
	if close {
		return s.sendJSONPriority(msg)
	}
	return s.sendJSON(msg)
}

func (s *LiveSession) sendJSON(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.enqueueNormal(outboundFrame{textPayload: payload})
}

func (s *LiveSession) sendJSONPriority(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.enqueuePriority(outboundFrame{textPayload: payload})
}

func (s *LiveSession) sendAssistantJSON(assistantID string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.enqueueNormal(outboundFrame{
		isAssistantAudio: true,
		assistantAudioID: assistantID,
		textPayload:      payload,
	})
}

func (s *LiveSession) sendAssistantBinaryPair(assistantID string, header any, data []byte) error {
	headerPayload, err := json.Marshal(header)
	if err != nil {
		return err
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	return s.enqueueNormal(outboundFrame{
		isAssistantAudio: true,
		assistantAudioID: assistantID,
		binaryPair:       &binaryPair{header: headerPayload, data: buf},
	})
}

func (s *LiveSession) enqueueNormal(frame outboundFrame) error {
	if frame.isAssistantAudio && s.isAssistantCanceled(frame.assistantAudioID) {
		return nil
	}
	select {
	case s.outboundNormal <- frame:
		return nil
	default:
		return errBackpressure
	}
}

func (s *LiveSession) enqueuePriority(frame outboundFrame) error {
	for i := 0; i < 4; i++ {
		select {
		case s.outboundPriority <- frame:
			return nil
		default:
		}
		select {
		case <-s.outboundPriority:
		default:
		}
	}
	select {
	case s.outboundPriority <- frame:
		return nil
	default:
		return errBackpressure
	}
}

func (s *LiveSession) readLoop(out chan<- inboundFrame) {
	defer close(out)
	for {
		messageType, data, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case out <- inboundFrame{err: err}:
			case <-s.ctx.Done():
			}
			return
		}
		select {
		case out <- inboundFrame{messageType: messageType, data: data}:
		case <-s.ctx.Done():
			return
		}
	}
}

func toolResultKey(turnID int, id string) string {
	return fmt.Sprintf("%d:%s", turnID, strings.TrimSpace(id))
}

func (s *LiveSession) registerToolResultWaiter(turnID int, id string) chan protocol.ClientToolResult {
	key := toolResultKey(turnID, id)
	ch := make(chan protocol.ClientToolResult, 1)
	s.toolWaitersMu.Lock()
	s.toolResultQueue[key] = ch
	s.toolWaitersMu.Unlock()
	return ch
}

func (s *LiveSession) unregisterToolResultWaiter(turnID int, id string) {
	key := toolResultKey(turnID, id)
	s.toolWaitersMu.Lock()
	delete(s.toolResultQueue, key)
	s.toolWaitersMu.Unlock()
}

func (s *LiveSession) dispatchToolResult(result protocol.ClientToolResult) bool {
	key := toolResultKey(result.TurnID, result.ID)
	s.toolWaitersMu.Lock()
	ch, ok := s.toolResultQueue[key]
	s.toolWaitersMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- result:
	default:
	}
	return true
}

func (s *LiveSession) executeClientToolCall(toolCtx, parentCtx context.Context, turnID int, call types.ToolUseBlock) ([]types.ContentBlock, *types.Error, error) {
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		return nil, &types.Error{
			Type:    string(core.ErrInvalidRequest),
			Message: "tool call id is required",
			Code:    "run_validation_failed",
			Param:   "tool_use.id",
		}, nil
	}
	callName := strings.TrimSpace(call.Name)
	waiter := s.registerToolResultWaiter(turnID, callID)
	defer s.unregisterToolResultWaiter(turnID, callID)

	if err := s.sendJSON(protocol.ServerToolCall{
		Type:   "tool_call",
		TurnID: turnID,
		ID:     callID,
		Name:   callName,
		Input:  call.Input,
	}); err != nil {
		return nil, nil, err
	}

	select {
	case result := <-waiter:
		content := result.Content
		if result.IsError {
			toolErr := result.Error
			if toolErr == nil {
				toolErr = &types.Error{
					Type:    string(core.ErrAPI),
					Message: "client tool failed",
					Code:    "tool_execution_failed",
				}
			}
			return content, toolErr, nil
		}
		return content, nil, nil
	case <-toolCtx.Done():
		reason := "tool_timeout"
		if parentCtx != nil && parentCtx.Err() != nil {
			reason = "turn_cancelled"
		}
		_ = s.sendJSON(protocol.ServerToolCancel{
			Type:   "tool_cancel",
			TurnID: turnID,
			ID:     callID,
			Reason: reason,
		})
		if parentCtx != nil && parentCtx.Err() != nil {
			return nil, nil, parentCtx.Err()
		}
		return nil, &types.Error{
			Type:    string(core.ErrAPI),
			Message: "client tool timed out",
			Code:    "tool_timeout",
		}, nil
	}
}

func (s *LiveSession) sessionTimeMS() int64 {
	if s == nil {
		return 0
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	if s.clockHaveClient.Load() {
		max := s.clockMaxClientMS.Load()
		at := s.clockMaxClientAtUnixNano.Load()
		elapsed := (now().UnixNano() - at) / int64(time.Millisecond)
		if elapsed < 0 {
			elapsed = 0
		}
		return max + elapsed
	}
	return now().Sub(s.startTime).Milliseconds()
}

func (s *LiveSession) observeClientTimestampMS(ts int64) {
	if s == nil || ts < 0 {
		return
	}
	for {
		current := s.clockMaxClientMS.Load()
		if ts <= current {
			return
		}
		if s.clockMaxClientMS.CompareAndSwap(current, ts) {
			now := time.Now
			if s.now != nil {
				now = s.now
			}
			s.clockMaxClientAtUnixNano.Store(now().UnixNano())
			s.clockHaveClient.Store(true)
			return
		}
	}
}

func (s *LiveSession) nextAssistantID() string {
	if s == nil {
		return ""
	}
	n := s.assistantCounter.Add(1)
	return fmt.Sprintf("a_%d", n)
}

func (s *LiveSession) Cancel() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *LiveSession) SendWarning(code, message string) error {
	if s == nil {
		return nil
	}
	return s.sendWarning(code, message)
}

func (s *LiveSession) newTurnContext() (context.Context, context.CancelFunc) {
	if s.cfg.TurnTimeout > 0 {
		return context.WithTimeout(s.ctx, s.cfg.TurnTimeout)
	}
	return context.WithCancel(s.ctx)
}

func (s *LiveSession) handleBackpressure(activeAssistantID string, activeTTSCancel *context.CancelFunc, activeRunCancel *context.CancelFunc) error {
	activeAssistantID = strings.TrimSpace(activeAssistantID)
	if activeAssistantID != "" {
		s.cancelAssistantAudio(activeAssistantID)
		_ = s.sendAudioReset("backpressure", activeAssistantID)
	}

	if activeTTSCancel != nil && *activeTTSCancel != nil {
		(*activeTTSCancel)()
		*activeTTSCancel = nil
	}
	if activeRunCancel != nil && *activeRunCancel != nil {
		(*activeRunCancel)()
		*activeRunCancel = nil
	}

	return errBackpressure
}

func (s *LiveSession) emitRunEvent(turnID int, event types.RunStreamEvent) error {
	if s == nil || !s.hello.Features.WantRunEvents || event == nil {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return s.sendJSON(protocol.ServerRunEvent{
		Type:   "run_event",
		TurnID: turnID,
		Event:  data,
	})
}

func (s *LiveSession) cancelAssistantAudio(assistantID string) {
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" {
		return
	}

	raw := s.canceledAssistant.Load()
	state, ok := raw.(canceledAssistantState)
	if !ok {
		state = canceledAssistantState{set: make(map[string]struct{}), order: nil}
	}
	if _, exists := state.set[assistantID]; exists {
		return
	}

	nextSet := make(map[string]struct{}, len(state.set)+1)
	for k := range state.set {
		nextSet[k] = struct{}{}
	}
	nextOrder := make([]string, 0, len(state.order)+1)
	nextOrder = append(nextOrder, state.order...)
	nextOrder = append(nextOrder, assistantID)
	nextSet[assistantID] = struct{}{}

	for len(nextOrder) > maxCanceledAssistantAudioIDs {
		evict := nextOrder[0]
		nextOrder = nextOrder[1:]
		delete(nextSet, evict)
	}

	s.canceledAssistant.Store(canceledAssistantState{set: nextSet, order: nextOrder})
}

func (s *LiveSession) isAssistantCanceled(assistantID string) bool {
	assistantID = strings.TrimSpace(assistantID)
	if assistantID == "" {
		return false
	}
	raw := s.canceledAssistant.Load()
	state, ok := raw.(canceledAssistantState)
	if !ok || state.set == nil {
		return false
	}
	_, exists := state.set[assistantID]
	return exists
}

func isMeaningfulTranscript(text, last string) bool {
	trimmed := normalizeSpace(text)
	if trimmed == "" {
		return false
	}
	if trimmed == normalizeSpace(last) {
		return false
	}
	return hasLetterOrDigit(trimmed)
}

func sttLanguageFromHello(hello protocol.ClientHello) string {
	if hello.Voice != nil {
		lang := strings.TrimSpace(hello.Voice.Language)
		if lang != "" {
			return lang
		}
	}
	return "en"
}

func byokForVoiceProvider(byok protocol.HelloBYOK, provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ""
	}
	if byok.Keys != nil {
		if key := strings.TrimSpace(byok.Keys[provider]); key != "" {
			return key
		}
	}
	switch provider {
	case protocol.VoiceProviderCartesia:
		return strings.TrimSpace(byok.Cartesia)
	case protocol.VoiceProviderElevenLabs:
		return strings.TrimSpace(byok.ElevenLabs)
	default:
		return ""
	}
}

func IsConfirmedSpeech(text string, isFinal bool, assistantSpeaking bool, clientHasAEC bool) bool {
	trimmed := normalizeSpace(text)
	if trimmed == "" {
		return false
	}
	if !hasLetterOrDigit(trimmed) {
		return false
	}

	minChars := 4
	if assistantSpeaking || !clientHasAEC {
		minChars = 8
	}
	if runeCount(trimmed) < minChars {
		return false
	}
	if assistantSpeaking && !isFinal && runeCount(trimmed) < 12 {
		return false
	}
	return true
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func runeCount(s string) int {
	return len([]rune(s))
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
